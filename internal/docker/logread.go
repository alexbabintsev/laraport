package docker

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// Log files are read by byte offset rather than by line number, so opening a
// multi-gigabyte log and scrolling back through it never scans the file from
// the start: every read is a seek plus a bounded chunk.

// LogChunkBytes is how much of a file is read per lazy chunk.
const LogChunkBytes = 256 << 10

// maxLogChunkBytes caps how far a chunk grows to find a line boundary when a
// single line is longer than LogChunkBytes.
const maxLogChunkBytes = 8 << 20

// readBlock is the dd block size; reads are block-aligned so dd can seek.
const readBlock = 4096

// LogPosition describes where a file tail started.
type LogPosition struct {
	// Start is the byte offset of the first line delivered.
	Start int64
	// End is the offset where following begins: every byte before it was
	// delivered as one of the InitialLines lines.
	End int64
	// InitialLines is how many lines of the stream precede the followed ones.
	InitialLines int
}

// FileSize returns the size of a file inside the container (stat, so the
// file is not read).
func FileSize(r Runner, containerID, path string) (int64, error) {
	q := shellQuote(path)
	out, err := runOutput(r, ExecShCmd(containerID, "stat -c %s "+q+" 2>/dev/null || wc -c < "+q))
	if err != nil {
		return 0, fmt.Errorf("size of %s: %w", path, err)
	}
	n, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("size of %s: unexpected output %q", path, out)
	}
	return n, nil
}

// readRangeScript prints bytes [start, end) of path, rounded out to whole
// readBlock blocks: dd skips with a seek, so the cost is independent of the
// offset. Where dd is missing, tail -c/head -c is used instead.
func readRangeScript(path string, start, end int64) (script string, skipped int64) {
	skip := start / readBlock
	count := (end+readBlock-1)/readBlock - skip
	q := shellQuote(path)
	from := skip * readBlock
	return fmt.Sprintf(
		`if command -v dd >/dev/null 2>&1; then dd if=%s bs=%d skip=%d count=%d 2>/dev/null; `+
			`else tail -c +%d %s | head -c %d; fi`,
		q, readBlock, skip, count, from+1, q, count*readBlock), from
}

// readRange returns bytes [start, end) of a file inside the container.
func readRange(r Runner, containerID, path string, start, end int64) ([]byte, error) {
	if end <= start {
		return nil, nil
	}
	script, from := readRangeScript(path, start, end)
	out, err := r.RunOutput(ExecShCmd(containerID, script), "")
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	data := []byte(out)
	off := start - from
	if off >= int64(len(data)) {
		return nil, nil
	}
	data = data[off:]
	if int64(len(data)) > end-start {
		data = data[:end-start]
	}
	return data, nil
}

// ReadLinesBefore reads the complete lines that end at or before byte offset
// end (which must be a line boundary, e.g. a previous chunk's start) using a
// chunk of about want bytes. It returns the lines and the offset where the
// first returned line starts; 0 means the beginning of the file was reached.
// A partial line at the start of the chunk is left for the next call, growing
// the chunk (up to maxLogChunkBytes) when a single line is longer than want.
func ReadLinesBefore(r Runner, containerID, path string, end, want int64) ([]string, int64, error) {
	if end <= 0 {
		return nil, 0, nil
	}
	if want <= 0 {
		want = LogChunkBytes
	}
	for {
		start := max(0, end-want)
		data, err := readRange(r, containerID, path, start, end)
		if err != nil {
			return nil, end, err
		}
		if start > 0 {
			i := bytes.IndexByte(data, '\n')
			if i < 0 || i == len(data)-1 {
				// No complete line in the chunk: widen it, unless it is already
				// at the cap — then return the fragment as it is.
				if want < maxLogChunkBytes {
					want *= 2
					continue
				}
				return splitLines(data), start, nil
			}
			data = data[i+1:]
			start += int64(i + 1)
		}
		return splitLines(data), start, nil
	}
}

// splitLines splits data into lines without their "\n" / "\r\n" endings.
func splitLines(data []byte) []string {
	data = bytes.TrimSuffix(data, []byte("\n"))
	if len(data) == 0 {
		return nil
	}
	parts := strings.Split(string(data), "\n")
	for i, p := range parts {
		parts[i] = strings.TrimSuffix(p, "\r")
	}
	return parts
}

// tailFollowBytes follows path from byte offset (0-based) inside the container.
func tailFollowBytes(containerID, path string, offset int64) HostCommand {
	return ExecStreamScript(containerID, fmt.Sprintf("tail -c +%d -f %s", offset+1, shellQuote(path)))
}

// TailLogFile shows the end of a file and then follows it. The last
// LogChunkBytes are read by seeking (instant even for huge files) and cut at
// the last complete line; following starts exactly there, so no line is lost
// or split. Earlier lines are loaded on demand with ReadLinesBefore starting
// from pos.Start. pos is nil when the size could not be determined; then a
// plain `tail -n 1000 -f` is used without positions.
func TailLogFile(r Runner, containerID, filePath string) (<-chan string, func(), *LogPosition, error) {
	size, err := FileSize(r, containerID, filePath)
	if err != nil {
		ch, stop, err := Stream(r, ExecStreamScript(containerID, "tail -n 1000 -f "+shellQuote(filePath)))
		return ch, stop, nil, err
	}

	// Initial chunk, cut at the last newline so a line still being written
	// arrives whole through the follow stream, and at the first newline (when
	// not at the file start) to drop the partial line that began earlier.
	var initial []string
	start, end := size, size
	if size > 0 {
		chunkStart := max(0, size-LogChunkBytes)
		data, err := readRange(r, containerID, filePath, chunkStart, size)
		if err != nil {
			return nil, nil, nil, err
		}
		if nl := bytes.LastIndexByte(data, '\n'); nl < 0 {
			// Not even one complete line in the chunk: just follow from it.
			start, end = chunkStart, chunkStart
		} else {
			data = data[:nl+1]
			start, end = chunkStart, chunkStart+int64(nl)+1
			if chunkStart > 0 {
				i := bytes.IndexByte(data, '\n')
				data = data[i+1:]
				start = chunkStart + int64(i) + 1
			}
			initial = splitLines(data)
		}
	}

	tailCh, tailStop, err := Stream(r, tailFollowBytes(containerID, filePath, end))
	if err != nil {
		return nil, nil, nil, err
	}

	// Merge: initial lines first, then the follow stream. quit unblocks the
	// merger when the consumer stops reading before the stream ends.
	ch := make(chan string, 128)
	quit := make(chan struct{})
	var once sync.Once
	stop := func() {
		once.Do(func() { close(quit) })
		tailStop()
	}
	go func() {
		defer close(ch)
		for _, line := range initial {
			select {
			case ch <- line:
			case <-quit:
				return
			}
		}
		for line := range tailCh {
			select {
			case ch <- line:
			case <-quit:
				return
			}
		}
	}()

	return ch, stop, &LogPosition{Start: start, End: end, InitialLines: len(initial)}, nil
}

// CountLines counts the lines of each file in the background, emitting one
// "N|path" line (path last) as each count finishes, in the order given. It
// ends on its own when done; stop it early when the counts are no longer
// needed (counting a multi-GB file reads all of it).
func CountLines(r Runner, containerID string, paths []string) (<-chan string, func(), error) {
	if len(paths) == 0 {
		ch := make(chan string)
		close(ch)
		return ch, func() {}, nil
	}
	quoted := make([]string, len(paths))
	for i, p := range paths {
		quoted[i] = shellQuote(p)
	}
	script := `for f in ` + strings.Join(quoted, " ") + `; do ` +
		`n=$(wc -l < "$f" 2>/dev/null) && printf '%s|%s\n' "$(echo $n)" "$f"; done`
	return Stream(r, ExecStreamScript(containerID, script))
}

// ParseLineCount parses one CountLines output line.
func ParseLineCount(line string) (path string, lines int, ok bool) {
	n, p, found := strings.Cut(line, "|")
	if !found || p == "" {
		return "", 0, false
	}
	v, err := strconv.Atoi(strings.TrimSpace(n))
	if err != nil {
		return "", 0, false
	}
	return p, v, true
}
