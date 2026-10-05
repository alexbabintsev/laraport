package docker

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexbabintsev/laraport/internal/connection"
)

// fakeProcess is a processStream over fixed data.
type fakeProcess struct {
	stdout  io.Reader
	stderr  string
	waitErr error
	stopped bool
}

func (p *fakeProcess) Stdout() io.Reader { return p.stdout }
func (p *fakeProcess) Stderr() string    { return p.stderr }
func (p *fakeProcess) Wait() error       { return p.waitErr }
func (p *fakeProcess) Stop()             { p.stopped = true }

func emitAll(string) bool { return true }

func listDir(t *testing.T, dir string) []string {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestReceiveSuccess(t *testing.T) {
	dir := t.TempDir()
	p := &fakeProcess{stdout: strings.NewReader("payload")}
	path, n, err := receive(p, dir, "out.tar.gz", nil, emitAll)
	if err != nil || n != 7 || path != filepath.Join(dir, "out.tar.gz") {
		t.Fatalf("got %q %d %v", path, n, err)
	}
	data, _ := os.ReadFile(path)
	st, _ := os.Stat(path)
	if string(data) != "payload" || st.Mode().Perm() != 0o600 {
		t.Fatalf("data %q mode %v", data, st.Mode().Perm())
	}
	if names := listDir(t, dir); len(names) != 1 {
		t.Fatalf("dir = %q", names)
	}
}

func TestReceiveNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "db.sql.gz"), []byte("old"), 0o644)
	os.WriteFile(filepath.Join(dir, "db_1.sql.gz"), []byte("old1"), 0o644)
	path, _, err := receive(&fakeProcess{stdout: strings.NewReader("new")}, dir, "db.sql.gz", nil, emitAll)
	if err != nil || filepath.Base(path) != "db_2.sql.gz" {
		t.Fatalf("path %q err %v", path, err)
	}
	if old, _ := os.ReadFile(filepath.Join(dir, "db.sql.gz")); string(old) != "old" {
		t.Fatal("existing file overwritten")
	}
}

func TestReceiveFailures(t *testing.T) {
	cases := []struct {
		name string
		p    *fakeProcess
		want string
	}{
		{"remote failure", &fakeProcess{stdout: strings.NewReader("partial"), waitErr: errors.New("exit status 1: boom")}, "remote command failed"},
		{"no data", &fakeProcess{stdout: strings.NewReader("")}, "no data received"},
		{"read error", &fakeProcess{stdout: io.MultiReader(strings.NewReader("x"), errReader{})}, "receiving data"},
	}
	for _, c := range cases {
		dir := t.TempDir()
		_, _, err := receive(c.p, dir, "f.bin", nil, emitAll)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v", c.name, err)
		}
		if names := listDir(t, dir); len(names) != 0 {
			t.Errorf("%s: left %q", c.name, names)
		}
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("network down") }

func TestReceiveCancelledByConsumer(t *testing.T) {
	dir := t.TempDir()
	p := &fakeProcess{stdout: strings.NewReader(strings.Repeat("x", 1<<20))}
	calls := 0
	emit := func(string) bool { calls++; return false } // consumer gone
	_, _, err := receive(p, dir, "f.bin", nil, emit)
	if err == nil || !p.stopped {
		t.Fatalf("err = %v stopped = %v", err, p.stopped)
	}
	if names := listDir(t, dir); len(names) != 0 {
		t.Fatalf("left %q", names)
	}
}

func TestPlaceFileExtensions(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.tar.gz", "a.tar.gz", "b.rdb", "b.rdb", "noext", "noext"} {
		tmp := filepath.Join(dir, ".tmp")
		os.WriteFile(tmp, []byte("x"), 0o600)
		if _, err := placeFile(tmp, dir, name); err != nil {
			t.Fatal(err)
		}
	}
	got := strings.Join(listDir(t, dir), ",")
	if got != "a.tar.gz,a_1.tar.gz,b.rdb,b_1.rdb,noext,noext_1" {
		t.Fatalf("files = %s", got)
	}
}

func TestFilterLines(t *testing.T) {
	long := strings.Repeat("y", 200_000) // longer than the 64 KB read buffer
	in := "keep 1\n\\restrict abc\n" + long + "\\restrict not-at-start\nkeep 2\n\\unrestrict abc\nlast no newline"
	var out bytes.Buffer
	if err := filterLines(&out, strings.NewReader(in), isRestrictLine); err != nil {
		t.Fatal(err)
	}
	want := "keep 1\n" + long + "\\restrict not-at-start\nkeep 2\nlast no newline"
	if out.String() != want {
		t.Fatalf("filtered output differs (len %d vs %d)", out.Len(), len(want))
	}
}

func TestStripRestrictLines(t *testing.T) {
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	io.WriteString(zw, "\\restrict k\nCREATE TABLE t();\n\\unrestrict k\n")
	zw.Close()

	var out bytes.Buffer
	if err := stripRestrictLines(&out, &gz); err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(&out)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(zr)
	if string(data) != "CREATE TABLE t();\n" {
		t.Fatalf("data = %q", data)
	}
	if err := stripRestrictLines(io.Discard, strings.NewReader("not gzip")); err == nil {
		t.Fatal("want error for non-gzip input")
	}
}

func TestProgressWriterThrottles(t *testing.T) {
	var lines []string
	pw := &progressWriter{w: io.Discard, emit: func(s string) bool { lines = append(lines, s); return true }}
	for i := 0; i < 1000; i++ {
		pw.Write(make([]byte, 1024))
	}
	if pw.n != 1024*1000 {
		t.Fatalf("n = %d", pw.n)
	}
	if len(lines) != 1 || !strings.HasPrefix(lines[0], StatusLinePrefix) {
		t.Fatalf("lines = %q", lines)
	}
}

func TestDownloadEndToEnd(t *testing.T) {
	dir := useTempDownloads(t)
	r := connection.NewLocalClient()

	ch, stop, err := download(r, HostCommand{Cmd: HostShCmd(`printf 'file-bytes'; echo "warn" >&2`)}, "go", "x.bin", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	lines := drain(t, ch, 10*time.Second)
	// Warnings come before the final "Saved to" line.
	if lines[0] != "go" || lines[len(lines)-2] != "warning: warn" || !strings.HasPrefix(lines[len(lines)-1], "Saved to: "+filepath.Join(dir, "x.bin")) {
		t.Fatalf("lines = %q", lines)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "x.bin")); string(data) != "file-bytes" {
		t.Fatalf("data = %q", data)
	}

	// Failure: the stderr of the failed command is reported.
	ch, stop2, err := download(r, HostCommand{Cmd: HostShCmd(`echo "pg_dump: no such db" >&2; exit 1`)}, "go", "y.bin", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer stop2()
	lines = drain(t, ch, 10*time.Second)
	if last := lines[len(lines)-1]; !strings.Contains(last, "no such db") {
		t.Fatalf("lines = %q", lines)
	}
	if _, err := os.Stat(filepath.Join(dir, "y.bin")); err == nil {
		t.Fatal("failed download saved a file")
	}
}

func TestDownloadStopBeforeReading(t *testing.T) {
	useTempDownloads(t)
	r := connection.NewLocalClient()
	ch, stop, err := download(r, HostCommand{Cmd: HostShCmd(`exec cat /dev/zero`)}, "go", "z.bin", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = ch // never read: stop must still return promptly
	done := make(chan struct{})
	go func() { stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stop blocked")
	}
	stop() // idempotent
}

func TestDownloadsDirError(t *testing.T) {
	old := downloadsDirFunc
	downloadsDirFunc = func() (string, error) { return "", errors.New("no home") }
	defer func() { downloadsDirFunc = old }()
	if _, _, err := download(connection.NewLocalClient(), HostCommand{Cmd: "true"}, "", "f", nil); err == nil {
		t.Fatal("want error")
	}
}
