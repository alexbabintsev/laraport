package connection

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestLocalRunCommandCombined(t *testing.T) {
	c := NewLocalClient()
	out, err := c.RunCommand(interleaved)
	if err != nil {
		t.Fatal(err)
	}
	if countPrefixed(out, "out-") != 3000 || countPrefixed(out, "err-") != 3000 {
		t.Fatal("combined output incomplete")
	}
}

func TestRunOutputSeparatesStderr(t *testing.T) {
	c := NewLocalClient()
	out, err := c.RunOutput(`echo data; echo noise >&2`, "")
	if err != nil || out != "data\n" {
		t.Fatalf("out %q err %v", out, err)
	}
	out, err = c.RunOutput(`echo partial; echo "it broke" >&2; exit 2`, "")
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 2 || !strings.Contains(err.Error(), "it broke") || out != "partial\n" {
		t.Fatalf("out %q err %v", out, err)
	}
	out, err = c.RunOutput(`cat`, "fed via stdin")
	if err != nil || out != "fed via stdin" {
		t.Fatalf("stdin: %q %v", out, err)
	}
}

func TestRunCommandInput(t *testing.T) {
	c := NewLocalClient()
	out, err := c.RunCommandInput(`IFS= read -r a; IFS= read -r b; echo "$b-$a"`, "one\ntwo\n")
	if err != nil || out != "two-one\n" {
		t.Fatalf("%q %v", out, err)
	}
}

func TestStreamCommandLines(t *testing.T) {
	c := NewLocalClient()
	ch, stop, err := c.StreamCommand(`printf 'a\r\nb\n\nlast'`, "")
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	var got []string
	for l := range ch {
		got = append(got, l)
	}
	if strings.Join(got, "|") != "a|b||last" {
		t.Fatalf("got %q", got)
	}
}

func TestStreamCommandLongLines(t *testing.T) {
	c := NewLocalClient()
	// One 2.5 MiB line followed by a short one: previously a 64 KB scanner
	// limit silently ended the stream at the long line.
	ch, stop, err := c.StreamCommand(`head -c 2621440 /dev/zero | tr '\0' x; echo; echo tail`, "")
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	total := 0
	var lines []string
	for l := range ch {
		total += len(l)
		lines = append(lines, l[:min(4, len(l))])
	}
	if total != 2621440+4 || lines[len(lines)-1] != "tail" {
		t.Fatalf("total %d, %d chunks, last %q", total, len(lines), lines[len(lines)-1])
	}
	if len(lines) != 4 { // 1 MiB + 1 MiB + 0.5 MiB + "tail"
		t.Fatalf("chunks = %d", len(lines))
	}
}

func TestStreamCommandStopKillsProcessGroup(t *testing.T) {
	c := NewLocalClient()
	pidFile := filepath.Join(t.TempDir(), "pid")
	// A pipeline: the grandchild must die too.
	ch, stop, err := c.StreamCommand(`sh -c 'echo $$ > `+pidFile+`; exec sleep 300' | cat & echo started; wait`, "")
	if err != nil {
		t.Fatal(err)
	}
	if l := <-ch; l != "started" {
		t.Fatalf("line %q", l)
	}
	var pid int
	waitUntil(t, func() bool {
		data, err := os.ReadFile(pidFile)
		pid, _ = strconv.Atoi(strings.TrimSpace(string(data)))
		return err == nil && pid > 0
	})
	stop()
	waitUntil(t, func() bool { return syscall.Kill(pid, 0) != nil })
	stop() // idempotent
}

func TestStreamCommandStopWithoutReading(t *testing.T) {
	c := NewLocalClient()
	_, stop, err := c.StreamCommand(`yes`, "")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond) // let the channel buffer fill up
	done := make(chan struct{})
	go func() { stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stop blocked with an unread channel")
	}
}

func TestStreamCommandInputThenEOFOnStop(t *testing.T) {
	c := NewLocalClient()
	marker := filepath.Join(t.TempDir(), "eof")
	ch, stop, err := c.StreamCommand(`IFS= read -r s; echo "got $s"; read _; touch `+marker+`; sleep 5`, "secret\n")
	if err != nil {
		t.Fatal(err)
	}
	if l := <-ch; l != "got secret" {
		t.Fatalf("line %q", l)
	}
	// stdin stays open while streaming
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("stdin closed before stop")
	}
	stop()
}

func TestInteractiveCommand(t *testing.T) {
	c := NewLocalClient()
	out, in, stop, err := c.InteractiveCommand(`while IFS= read -r l; do echo "echo:$l"; done`)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	in <- "hello"
	if l := <-out; l != "echo:hello" {
		t.Fatalf("got %q", l)
	}
	in <- "world"
	if l := <-out; l != "echo:world" {
		t.Fatalf("got %q", l)
	}
	stop()
	// Output closes after stop; sends after stop must not block forever.
	waitUntil(t, func() bool {
		select {
		case _, ok := <-out:
			return !ok
		default:
			return false
		}
	})
}

func TestStartCommandProcess(t *testing.T) {
	c := NewLocalClient()
	p, err := c.StartCommand(`cat; echo warn >&2; exit 3`, "binary\x00data")
	if err != nil {
		t.Fatal(err)
	}
	// stdin stays open until the process ends or is stopped; cat needs EOF,
	// so read what arrives and then stop.
	buf := make([]byte, 11)
	if _, err := io.ReadFull(p.Stdout(), buf); err != nil || string(buf) != "binary\x00data" {
		t.Fatalf("stdout %q %v", buf, err)
	}
	p.Stop()
	if err := p.Wait(); err == nil {
		t.Fatal("want error after stop")
	}
	p.Stop() // idempotent

	p, err = c.StartCommand(`echo out; echo "details here" >&2; exit 4`, "")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(p.Stdout())
	err = p.Wait()
	if string(data) != "out\n" || err == nil || !strings.Contains(err.Error(), "details here") || p.Stderr() != "details here\n" {
		t.Fatalf("data %q err %v stderr %q", data, err, p.Stderr())
	}
}

func TestLocalCloseIsNoop(t *testing.T) {
	if err := NewLocalClient().Close(); err != nil {
		t.Fatal(err)
	}
}

func TestTailBuffer(t *testing.T) {
	tb := &tailBuffer{max: 5}
	tb.Write([]byte("abc"))
	tb.Write([]byte("defgh"))
	if tb.String() != "defgh" {
		t.Fatalf("got %q", tb.String())
	}
	tb.Write([]byte("0123456789"))
	if tb.String() != "56789" {
		t.Fatalf("got %q", tb.String())
	}
}

func TestReadLinesStopsWhenEmitFalse(t *testing.T) {
	n := 0
	readLines(strings.NewReader("a\nb\nc\n"), func(string) bool { n++; return n < 2 })
	if n != 2 {
		t.Fatalf("emit called %d times", n)
	}
	var got []string
	readLines(strings.NewReader(""), func(l string) bool { got = append(got, l); return true })
	if len(got) != 0 {
		t.Fatalf("empty input produced %q", got)
	}
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
