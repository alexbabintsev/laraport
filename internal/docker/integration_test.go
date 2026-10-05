package docker

// Integration tests against a real Docker daemon. They create throwaway
// containers labelled laradok-test and remove them afterwards.
//
// Run with: LARADOK_INTEGRATION=1 go test ./internal/docker/

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexbabintsev/laradok/internal/connection"
)

func requireIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("LARADOK_INTEGRATION") != "1" {
		t.Skip("set LARADOK_INTEGRATION=1 to run Docker integration tests")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skipf("docker not available: %v", err)
	}
}

// startContainer runs image detached and returns its ID.
func startContainer(t *testing.T, image string, args ...string) string {
	t.Helper()
	full := append([]string{"run", "-d", "--label", "laradok-test=1"}, args...)
	// args may contain "--" separating docker flags from the image command.
	var flags, cmd []string
	split := -1
	for i, a := range full {
		if a == "--" {
			split = i
			break
		}
	}
	if split >= 0 {
		flags, cmd = full[:split], full[split+1:]
	} else {
		flags = full
	}
	runArgs := append(append(flags, image), cmd...)
	out, err := exec.Command("docker", runArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker run %s: %v\n%s", image, err, out)
	}
	id := strings.TrimSpace(string(out))
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", id).Run() }) //nolint:errcheck
	return id
}

// dexec runs a command in the container directly (test setup).
func dexec(t *testing.T, id string, script string) string {
	t.Helper()
	out, err := exec.Command("docker", "exec", id, "sh", "-c", script).CombinedOutput()
	if err != nil {
		t.Fatalf("docker exec %q: %v\n%s", script, err, out)
	}
	return string(out)
}

// waitFor polls cond until it returns true or the timeout expires.
func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// processCount counts processes in the container whose command line contains
// needle (using /proc, so it works without ps).
func processCount(t *testing.T, id, needle string) int {
	t.Helper()
	script := `n=0; for p in /proc/[0-9]*; do c=$(tr '\0' ' ' < $p/cmdline 2>/dev/null); case "$c" in *` +
		shellQuote(needle) + `*) case "$c" in *"for p in"*) ;; *) n=$((n+1));; esac;; esac; done; echo $n`
	out := strings.TrimSpace(dexec(t, id, script))
	var n int
	fmt.Sscanf(out, "%d", &n)
	return n
}

// shells are base images whose /bin/sh differs: busybox ash, dash and bash.
var shells = []string{"alpine:3.20", "debian:bookworm-slim", "fedora:latest"}

func withShells(t *testing.T, f func(t *testing.T, r Runner, id string)) {
	runShells(t, true, f)
}

// withShellsSerial is withShells for tests that override package globals
// (the downloads directory).
func withShellsSerial(t *testing.T, f func(t *testing.T, r Runner, id string)) {
	runShells(t, false, f)
}

func runShells(t *testing.T, parallel bool, f func(t *testing.T, r Runner, id string)) {
	requireIntegration(t)
	for _, img := range shells {
		img := img
		t.Run(img, func(t *testing.T) {
			if parallel {
				t.Parallel()
			}
			id := startContainer(t, img, "--", "sleep", "3600")
			f(t, connection.NewLocalClient(), id)
		})
	}
}

// useTempDownloads redirects downloads into a temp dir for the test.
func useTempDownloads(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	old := downloadsDirFunc
	downloadsDirFunc = func() (string, error) { return dir, nil }
	t.Cleanup(func() { downloadsDirFunc = old })
	return dir
}

// drain collects a stream until it closes or the timeout expires.
func drain(t *testing.T, ch <-chan string, timeout time.Duration) []string {
	t.Helper()
	var lines []string
	timer := time.After(timeout)
	for {
		select {
		case l, ok := <-ch:
			if !ok {
				return lines
			}
			lines = append(lines, l)
		case <-timer:
			t.Fatalf("stream did not finish; got %q", lines)
		}
	}
}

func savedPath(t *testing.T, lines []string) string {
	t.Helper()
	for _, l := range lines {
		if strings.HasPrefix(l, "ERROR") {
			t.Fatalf("download failed: %q", lines)
		}
		if rest, ok := strings.CutPrefix(l, "Saved to: "); ok {
			return rest[:strings.LastIndex(rest, " (")]
		}
	}
	t.Fatalf("no Saved line in %q", lines)
	return ""
}

func TestIntegrationHostileLogFileNames(t *testing.T) {
	withShells(t, func(t *testing.T, r Runner, id string) {
		names := []string{
			"plain.log",
			"with space.log",
			"quote'q.log",
			`x$(echo${IFS}INJECTED).log`,
			"back`tick`.log",
			"pipe|name.log",
		}
		dexec(t, id, "mkdir -p /var/www/html/storage/logs")
		for i, n := range names {
			p := "/var/www/html/storage/logs/" + n
			// Write via the test's own quoting (independent of the code under test).
			dexec(t, id, fmt.Sprintf("i=0; while [ $i -lt %d ]; do echo line$i; i=$((i+1)); done > %s", i+1, shellQuote(p)))
		}

		files, err := ListLogFiles(r, id, "")
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]LogFileInfo{}
		var paths []string
		for _, f := range files {
			got[filepath.Base(f.Path)] = f
			paths = append(paths, f.Path)
		}
		counts := map[string]int{}
		ch, stop, err := CountLines(r, id, paths)
		if err != nil {
			t.Fatal(err)
		}
		for line := range ch {
			if p, n, ok := ParseLineCount(line); ok {
				counts[filepath.Base(p)] = n
			}
		}
		stop()
		for i, n := range names {
			f, ok := got[n]
			if !ok {
				t.Fatalf("missing %q in %v", n, files)
			}
			if counts[n] != i+1 {
				t.Errorf("%q: counted %d lines, want %d", n, counts[n], i+1)
			}
			// The path must reach the container verbatim for these, too.
			size, err := FileSize(r, id, f.Path)
			if err != nil || size != f.Size || size == 0 {
				t.Errorf("FileSize(%q) = %d, %v (listed %d)", n, size, err, f.Size)
			}
			lines, start, err := ReadLinesBefore(r, id, f.Path, size, LogChunkBytes)
			if err != nil || len(lines) != i+1 || start != 0 {
				t.Errorf("ReadLinesBefore(%q) = %d lines from %d, %v", n, len(lines), start, err)
			}
		}
	})
}

func TestIntegrationCustomCommandRunsInContainer(t *testing.T) {
	withShells(t, func(t *testing.T, r Runner, id string) {
		dexec(t, id, "mkdir -p /srv/app")
		ch, stop, err := ExecCustomCommand(r, id, "/srv/app", `echo "host=$HOSTNAME pwd=$(pwd)"`)
		if err != nil {
			t.Fatal(err)
		}
		defer stop()
		lines := drain(t, ch, 10*time.Second)
		want := fmt.Sprintf("host=%s pwd=/srv/app", id[:12])
		if len(lines) != 1 || lines[0] != want {
			t.Fatalf("got %q, want %q", lines, want)
		}
	})
}

func TestIntegrationStreamStopKillsContainerProcess(t *testing.T) {
	withShells(t, func(t *testing.T, r Runner, id string) {
		ch, stop, err := ExecCustomCommand(r, id, "", `echo started; sleep 987`)
		if err != nil {
			t.Fatal(err)
		}
		if l := <-ch; l != "started" {
			t.Fatalf("first line %q", l)
		}
		// The wrapper shell carries the script in its argv too, so count ≥ 1.
		waitFor(t, 5*time.Second, "sleep to start", func() bool { return processCount(t, id, "sleep 987") >= 1 })
		stop()
		waitFor(t, 10*time.Second, "sleep to be killed", func() bool { return processCount(t, id, "sleep 987") == 0 })
	})
}

func TestIntegrationStreamEndsOnItsOwn(t *testing.T) {
	withShells(t, func(t *testing.T, r Runner, id string) {
		ch, stop, err := ExecCustomCommand(r, id, "", `echo a; echo b >&2; exit 3`)
		if err != nil {
			t.Fatal(err)
		}
		defer stop()
		lines := drain(t, ch, 10*time.Second)
		if strings.Join(lines, ",") != "a,b" && strings.Join(lines, ",") != "b,a" {
			t.Fatalf("lines = %q", lines)
		}
	})
}

func TestIntegrationTailLogFile(t *testing.T) {
	withShells(t, func(t *testing.T, r Runner, id string) {
		p := "/var/log/app log.log"
		// 40000 lines of 8 bytes ("lineNNNNN\n" padded) ≈ 400 KB: more than one chunk.
		dexec(t, id, fmt.Sprintf("i=10000; while [ $i -lt 50000 ]; do echo line$i; i=$((i+1)); done > %s", shellQuote(p)))
		const lineLen = 10 // "line12345\n"

		ch, stop, pos, err := TailLogFile(r, id, p)
		if err != nil {
			t.Fatal(err)
		}
		size := int64(40000 * lineLen)
		if pos == nil || pos.End != size || pos.Start%lineLen != 0 || size-pos.Start > LogChunkBytes {
			t.Fatalf("pos = %+v", pos)
		}
		first := 10000 + int(pos.Start/lineLen)
		for i := 0; i < pos.InitialLines; i++ {
			if l := <-ch; l != fmt.Sprintf("line%d", first+i) {
				t.Fatalf("initial line %d = %q", i, l)
			}
		}
		dexec(t, id, fmt.Sprintf("echo appended >> %s", shellQuote(p)))
		select {
		case l := <-ch:
			if l != "appended" {
				t.Fatalf("followed line = %q", l)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("appended line not followed")
		}

		// Walk back to the start of the file chunk by chunk.
		end, prev := pos.Start, first
		for end > 0 {
			lines, start, err := ReadLinesBefore(r, id, p, end, LogChunkBytes)
			if err != nil || len(lines) == 0 {
				t.Fatalf("chunk before %d: %d lines, %v", end, len(lines), err)
			}
			if lines[len(lines)-1] != fmt.Sprintf("line%d", prev-1) {
				t.Fatalf("chunk before %d ends with %q, want line%d", end, lines[len(lines)-1], prev-1)
			}
			prev -= len(lines)
			end = start
		}
		if prev != 10000 {
			t.Fatalf("walked back to line%d, want line10000", prev)
		}

		stop()
		waitFor(t, 10*time.Second, "tail to exit", func() bool { return processCount(t, id, "tail -c +") == 0 })
	})
}

// TestIntegrationLargeLogIsInstant checks that opening and scrolling a log
// does not depend on its size: the same operations on a 1 MB and a 1 GB file
// must take about as long (each is a seek plus a bounded chunk), whereas
// anything that scans the file grows ~1000×. (Absolute times are not useful
// here: the file sits in the page cache, so even a full read is fast.)
func TestIntegrationLargeLogIsInstant(t *testing.T) {
	requireIntegration(t)
	r := connection.NewLocalClient()
	id := startContainer(t, "debian:bookworm-slim", "--", "sleep", "3600")
	line := "'a moderately long log line with some context [2026-10-05 12:00:00] production.ERROR'"
	dexec(t, id, "yes "+line+" | head -c 1048576 > /var/log/small.log")
	dexec(t, id, "yes "+line+" | head -c 1073741824 > /var/log/huge.log")

	// ops opens the file, then reads one chunk from the middle and one near
	// the beginning; it returns the best of three runs to reduce noise.
	ops := func(p string) time.Duration {
		best := time.Duration(1 << 62)
		for run := 0; run < 3; run++ {
			t0 := time.Now()
			_, stop, pos, err := TailLogFile(r, id, p)
			if err != nil || pos == nil {
				t.Fatalf("%s: %v", p, err)
			}
			_, start, err := ReadLinesBefore(r, id, p, pos.Start/2, 4096)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := ReadLinesBefore(r, id, p, start, LogChunkBytes); err != nil {
				t.Fatal(err)
			}
			if _, _, err := ReadLinesBefore(r, id, p, min(pos.Start, 300000), LogChunkBytes); err != nil {
				t.Fatal(err)
			}
			d := time.Since(t0)
			stop()
			best = min(best, d)
		}
		return best
	}
	small, huge := ops("/var/log/small.log"), ops("/var/log/huge.log")
	t0 := time.Now()
	dexec(t, id, "wc -l < /var/log/huge.log")
	t.Logf("1 MB: %v   1 GB: %v   (wc -l on 1 GB: %v)", small, huge, time.Since(t0))
	if huge > 3*small+300*time.Millisecond {
		t.Errorf("log reads scale with file size: 1 MB %v vs 1 GB %v", small, huge)
	}
}

func tarEntries(t *testing.T, path string) map[string]int64 {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(zr)
	out := map[string]int64{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		out[strings.TrimSuffix(h.Name, "/")] = h.Size
	}
}

func assertNoPartFiles(t *testing.T, dir string) {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(dir, ".laradok-*.part"))
	if len(m) != 0 {
		t.Fatalf("temp files left behind: %v", m)
	}
}

func gunzipString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}
