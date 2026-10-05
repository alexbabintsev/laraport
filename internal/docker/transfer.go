package docker

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// StatusLinePrefix marks a streamed output line that the OutputScreen should
// render in place, overwriting the previous status line (e.g. a "received N MB"
// counter) instead of appending. It uses control bytes that never occur in real
// command output.
const StatusLinePrefix = "\x00status\x00"

// progressInterval throttles "received N MB" status updates.
const progressInterval = 500 * time.Millisecond

// downloadsDirFunc returns the directory downloads are saved to. Tests
// override it.
var downloadsDirFunc = func() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locating home directory: %w", err)
	}
	return filepath.Join(home, "Downloads"), nil
}

// timestamp is the suffix used in downloaded file names.
func timestamp() string { return time.Now().Format("20060102_150405") }

// safeFileName makes s usable as a single file name component.
func safeFileName(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '/' || r == '\\' || r == ' ' || r == ':' || r < 0x20 || r == 0x7f:
			return '_'
		}
		return r
	}, s)
	s = strings.Trim(s, ".")
	if s == "" {
		return "download"
	}
	return s
}

// filterFunc transforms the downloaded byte stream on its way to disk.
type filterFunc func(dst io.Writer, src io.Reader) error

// download runs hc, whose stdout is the raw file content, and streams it into
// <downloads dir>/<fileName>. Data goes straight to disk (constant memory) into
// a private temp file that is renamed into place only after the command exits
// successfully, so failed or cancelled transfers never leave a partial file.
//
// The returned channel carries startMsg, throttled progress status lines and a
// final "Saved to: …" or "ERROR: …" line. stop cancels the transfer (killing
// the remote command) and waits for cleanup; it is safe to call at any time.
func download(r Runner, hc HostCommand, startMsg, fileName string, filter filterFunc) (<-chan string, func(), error) {
	dir, err := downloadsDirFunc()
	if err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("creating %s: %w", dir, err)
	}
	p, err := r.StartCommand(hc.Cmd, hc.Input)
	if err != nil {
		return nil, nil, err
	}

	out := make(chan string, 16)
	quit := make(chan struct{})
	done := make(chan struct{})
	emit := func(line string) bool {
		select {
		case out <- line:
			return true
		case <-quit:
			return false
		}
	}

	go func() {
		defer close(done)
		defer close(out)
		if !emit(startMsg) {
			p.Stop()
			return
		}
		saved, size, err := receive(p, dir, fileName, filter, emit)
		select {
		case <-quit:
			return // cancelled: receive already cleaned up
		default:
		}
		if err != nil {
			emit("ERROR: " + err.Error())
			return
		}
		// Non-fatal diagnostics (e.g. tar "file changed as we read it").
		for _, w := range strings.Split(strings.TrimSpace(p.Stderr()), "\n") {
			if w = strings.TrimSpace(w); w != "" && !emit("warning: "+w) {
				return
			}
		}
		emit(fmt.Sprintf("Saved to: %s (%.2f MB)", saved, float64(size)/1024/1024))
	}()

	var once sync.Once
	stop := func() {
		once.Do(func() {
			close(quit)
			p.Stop()
		})
		<-done
	}
	return out, stop, nil
}

// processStream is the part of connection.Process that receive needs.
type processStream interface {
	Stdout() io.Reader
	Stderr() string
	Wait() error
	Stop()
}

// receive copies p's stdout (through filter, if any) into a temp file in dir
// and renames it to a unique name based on fileName. Returns the final path
// and the number of bytes written.
func receive(p processStream, dir, fileName string, filter filterFunc, emit func(string) bool) (string, int64, error) {
	tmp, err := os.CreateTemp(dir, ".laradok-*.part") // mode 0600
	if err != nil {
		p.Stop()
		return "", 0, fmt.Errorf("creating file: %w", err)
	}
	keep := false
	defer func() {
		if !keep {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()

	pw := &progressWriter{w: tmp, emit: emit}
	var copyErr error
	if filter != nil {
		copyErr = filter(pw, p.Stdout())
	} else {
		_, copyErr = io.Copy(pw, p.Stdout())
	}
	if copyErr != nil {
		p.Stop()
	}
	waitErr := p.Wait()
	switch {
	case waitErr != nil:
		return "", 0, fmt.Errorf("remote command failed: %w", waitErr)
	case copyErr != nil:
		return "", 0, fmt.Errorf("receiving data: %w", copyErr)
	case pw.n == 0:
		return "", 0, errors.New("no data received")
	}
	if err := tmp.Sync(); err != nil {
		return "", 0, fmt.Errorf("writing file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", 0, fmt.Errorf("writing file: %w", err)
	}
	final, err := placeFile(tmp.Name(), dir, fileName)
	if err != nil {
		return "", 0, err
	}
	keep = true
	return final, pw.n, nil
}

// placeFile renames tmp to dir/name, adding a numeric suffix instead of
// overwriting an existing file.
func placeFile(tmp, dir, name string) (string, error) {
	ext := ""
	for _, e := range []string{".tar.gz", ".sql.gz", ".archive.gz"} {
		if strings.HasSuffix(name, e) {
			ext = e
			break
		}
	}
	if ext == "" {
		ext = filepath.Ext(name)
	}
	stem := strings.TrimSuffix(name, ext)
	for i := 0; i < 1000; i++ {
		candidate := filepath.Join(dir, name)
		if i > 0 {
			candidate = filepath.Join(dir, fmt.Sprintf("%s_%d%s", stem, i, ext))
		}
		// Link fails if the target exists, so concurrent downloads cannot
		// clobber each other; fall back to rename where hard links are not
		// supported.
		if err := os.Link(tmp, candidate); err == nil {
			os.Remove(tmp)
			return candidate, nil
		} else if os.IsExist(err) {
			continue
		}
		if _, err := os.Lstat(candidate); err == nil {
			continue
		}
		if err := os.Rename(tmp, candidate); err != nil {
			return "", fmt.Errorf("saving file: %w", err)
		}
		return candidate, nil
	}
	return "", fmt.Errorf("saving file: too many files named %s", name)
}

// progressWriter counts bytes and emits a throttled "received" status line.
type progressWriter struct {
	w    io.Writer
	emit func(string) bool
	n    int64
	last time.Time
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	p.n += int64(n)
	if now := time.Now(); now.Sub(p.last) >= progressInterval {
		p.last = now
		if !p.emit(StatusLinePrefix + "  received " + humanBytes(p.n)) {
			return n, errors.New("cancelled")
		}
	}
	return n, err
}

// humanBytes formats a byte count as B/KB/MB/GB.
func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.2f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n/1024)
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// stripRestrictLines is a filterFunc for gzipped plain pg_dump output: it
// drops the `\restrict` / `\unrestrict` psql meta-command lines added by
// pg_dump 17.6+ (which older psql versions cannot restore) and re-compresses,
// streaming with constant memory.
func stripRestrictLines(dst io.Writer, src io.Reader) error {
	zr, err := gzip.NewReader(src)
	if err != nil {
		return fmt.Errorf("reading gzip stream: %w", err)
	}
	zw := gzip.NewWriter(dst)
	if err := filterLines(zw, zr, isRestrictLine); err != nil {
		return err
	}
	if err := zr.Close(); err != nil {
		return err
	}
	return zw.Close()
}

func isRestrictLine(line []byte) bool {
	return bytes.HasPrefix(line, []byte(`\restrict `)) || bytes.HasPrefix(line, []byte(`\unrestrict `))
}

// filterLines copies src to dst, dropping every line for which drop returns
// true. drop sees the beginning of the line (up to 64 KB); longer lines are
// copied through in fragments.
func filterLines(dst io.Writer, src io.Reader, drop func(line []byte) bool) error {
	br := bufio.NewReaderSize(src, 64<<10)
	atLineStart := true
	dropping := false
	for {
		frag, err := br.ReadSlice('\n')
		if len(frag) > 0 {
			if atLineStart {
				dropping = drop(frag)
			}
			if !dropping {
				if _, werr := dst.Write(frag); werr != nil {
					return werr
				}
			}
			atLineStart = frag[len(frag)-1] == '\n'
		}
		switch {
		case err == nil, errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			return nil
		default:
			return err
		}
	}
}
