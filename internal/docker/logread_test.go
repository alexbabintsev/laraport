package docker

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// makeLog writes content to a temp file and returns its path.
func makeLog(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "app log.log")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// sampleLog has short, long (> several dd blocks), empty, CRLF and
// multi-byte lines.
func sampleLog() (string, []string) {
	var lines []string
	for i := 0; i < 400; i++ {
		switch {
		case i%97 == 0:
			lines = append(lines, strings.Repeat("L", 9000+i)) // longer than a dd block
		case i%13 == 0:
			lines = append(lines, "")
		case i%7 == 0:
			lines = append(lines, "кириллица строка "+strings.Repeat("ж", i))
		default:
			lines = append(lines, strings.Repeat("x", i%50)+" line")
		}
	}
	var b strings.Builder
	for i, l := range lines {
		b.WriteString(l)
		if i%11 == 0 {
			b.WriteString("\r") // CRLF ending
		}
		b.WriteString("\n")
	}
	return b.String(), lines
}

func TestFileSize(t *testing.T) {
	_, r := fakeDocker(t)
	p := makeLog(t, "12345")
	if n, err := FileSize(r, "c", p); err != nil || n != 5 {
		t.Fatalf("size %d err %v", n, err)
	}
	if _, err := FileSize(r, "c", p+".missing"); err == nil {
		t.Fatal("want error for a missing file")
	}
}

func TestReadRangeMatchesFile(t *testing.T) {
	_, r := fakeDocker(t)
	content, _ := sampleLog()
	p := makeLog(t, content)
	data := []byte(content)
	size := int64(len(data))
	for _, rg := range [][2]int64{{0, 1}, {0, 4096}, {1, 4097}, {4095, 4097}, {5000, 20000}, {size - 10, size}, {size - 1, size + 100}, {7, 7}} {
		got, err := readRange(r, "c", p, rg[0], rg[1])
		if err != nil {
			t.Fatal(err)
		}
		end := min(rg[1], size)
		var want []byte
		if rg[0] < end {
			want = data[rg[0]:end]
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("range %v: got %d bytes, want %d", rg, len(got), len(want))
		}
	}
}

func TestReadRangeWithoutDD(t *testing.T) {
	dir, r := fakeDocker(t)
	// A PATH with sh, tail and head but no dd exercises the fallback branch.
	bin := filepath.Join(dir, "nodd")
	os.Mkdir(bin, 0o755)
	for _, tool := range []string{"sh", "tail", "head", "cat"} {
		for _, d := range []string{"/bin", "/usr/bin"} {
			if _, err := os.Stat(filepath.Join(d, tool)); err == nil {
				os.Symlink(filepath.Join(d, tool), filepath.Join(bin, tool))
				break
			}
		}
	}
	os.Symlink(mustLookDocker(t), filepath.Join(bin, "docker"))
	t.Setenv("PATH", bin)

	content, _ := sampleLog()
	p := makeLog(t, content)
	got, err := readRange(r, "c", p, 5000, 20000)
	if err != nil || !bytes.Equal(got, []byte(content)[5000:20000]) {
		t.Fatalf("fallback read: %d bytes, err %v", len(got), err)
	}
}

// TestReadLinesBeforeWalksWholeFile reads a file backwards in small chunks and
// checks the pieces reassemble it exactly — no line lost, split or repeated.
func TestReadLinesBeforeWalksWholeFile(t *testing.T) {
	_, r := fakeDocker(t)
	content, want := sampleLog()
	p := makeLog(t, content)
	for _, chunk := range []int64{100, 4096, 30000, LogChunkBytes} {
		end := int64(len(content))
		var got []string
		for steps := 0; end > 0; steps++ {
			if steps > 10000 {
				t.Fatal("no progress")
			}
			lines, start, err := ReadLinesBefore(r, "c", p, end, chunk)
			if err != nil {
				t.Fatal(err)
			}
			if start >= end {
				t.Fatalf("chunk %d: offset did not move back from %d", chunk, end)
			}
			got = append(lines, got...)
			end = start
		}
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Fatalf("chunk %d: reassembled file differs (%d vs %d lines)", chunk, len(got), len(want))
		}
	}
}

func TestReadLinesBeforeHugeLineIsCapped(t *testing.T) {
	_, r := fakeDocker(t)
	huge := strings.Repeat("H", maxLogChunkBytes+1000)
	p := makeLog(t, "first\n"+huge+"\n")
	end := int64(len("first\n" + huge + "\n"))
	lines, start, err := ReadLinesBefore(r, "c", p, end, 1024)
	if err != nil {
		t.Fatal(err)
	}
	// The line is longer than the cap: a fragment comes back, but progress is made.
	if start <= 0 || start >= end || len(lines) != 1 || len(lines[0]) > maxLogChunkBytes {
		t.Fatalf("start %d lines %d len %d", start, len(lines), len(lines[0]))
	}
	if lines, start, _ := ReadLinesBefore(r, "c", p, 0, 1024); lines != nil || start != 0 {
		t.Fatal("reading before offset 0 must return nothing")
	}
}

func TestTailLogFileBigFile(t *testing.T) {
	_, r := fakeDocker(t)
	var b strings.Builder
	for i := 0; i < 3000; i++ {
		b.WriteString(strings.Repeat("y", 150))
		b.WriteString("\n")
	}
	content := b.String()
	p := makeLog(t, content)

	ch, stop, pos, err := TailLogFile(r, "c", p)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	size := int64(len(content))
	if pos == nil || pos.End != size || pos.Start <= 0 || pos.Start%151 != 0 || size-pos.Start > LogChunkBytes {
		t.Fatalf("pos = %+v (size %d)", pos, size)
	}
	if want := int((size - pos.Start) / 151); pos.InitialLines != want {
		t.Fatalf("initial lines %d, want %d", pos.InitialLines, want)
	}
	for i := 0; i < pos.InitialLines; i++ {
		if l := <-ch; len(l) != 150 {
			t.Fatalf("initial line %d has length %d", i, len(l))
		}
	}
	appendTo(t, p, "appended\n")
	expectLine(t, ch, "appended")

	// The earlier part is reachable from pos.Start.
	lines, start, err := ReadLinesBefore(r, "c", p, pos.Start, LogChunkBytes)
	if err != nil || len(lines) == 0 || start >= pos.Start {
		t.Fatalf("earlier chunk: %d lines start %d err %v", len(lines), start, err)
	}
}

func TestTailLogFilePartialLastLine(t *testing.T) {
	_, r := fakeDocker(t)
	p := makeLog(t, "one\ntwo\nthree-part")
	ch, stop, pos, err := TailLogFile(r, "c", p)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if pos.Start != 0 || pos.End != 8 || pos.InitialLines != 2 {
		t.Fatalf("pos = %+v", pos)
	}
	expectLine(t, ch, "one")
	expectLine(t, ch, "two")
	appendTo(t, p, "-finished\n")
	// The line being written arrives whole, not split in two.
	expectLine(t, ch, "three-part-finished")
}

func TestTailLogFileEmptyAndMissing(t *testing.T) {
	_, r := fakeDocker(t)
	p := makeLog(t, "")
	ch, stop, pos, err := TailLogFile(r, "c", p)
	if err != nil || pos == nil || pos.Start != 0 || pos.End != 0 || pos.InitialLines != 0 {
		t.Fatalf("pos %+v err %v", pos, err)
	}
	appendTo(t, p, "first\n")
	expectLine(t, ch, "first")
	stop()

	// Size unknown: plain tail without positions.
	ch, stop, pos, err = TailLogFile(r, "c", p+".missing")
	if err != nil || pos != nil {
		t.Fatalf("missing file: pos %+v err %v", pos, err)
	}
	stop()
	_ = ch
}

func TestCountLinesEmpty(t *testing.T) {
	ch, stop, err := CountLines(nil, "c", nil)
	if err != nil {
		t.Fatal(err)
	}
	stop()
	if _, ok := <-ch; ok {
		t.Fatal("empty count should yield nothing")
	}
}

func appendTo(t *testing.T, path, s string) {
	t.Helper()
	time.Sleep(300 * time.Millisecond) // let tail -f start
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
}

func expectLine(t *testing.T, ch <-chan string, want string) {
	t.Helper()
	select {
	case l := <-ch:
		if l != want {
			t.Fatalf("line %q, want %q", l, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %q", want)
	}
}
