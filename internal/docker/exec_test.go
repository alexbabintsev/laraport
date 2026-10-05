package docker

import (
	"compress/gzip"
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

	"github.com/alexbabintsev/laradok/internal/connection"
)

// hostile is a set of strings that break naive shell quoting.
var hostile = []string{
	"",
	"plain",
	"with space",
	"it's",
	`double"quote`,
	`$(touch PWNED)`,
	"`touch PWNED`",
	"${HOME}",
	`back\slash`,
	"semi;colon && ls | wc",
	"new\nline",
	"tab\there",
	"'''",
	"*?[a]",
	"-n",
	"ünïcødé ✓",
}

func TestShellQuoteRoundTrip(t *testing.T) {
	dir := t.TempDir()
	for _, s := range hostile {
		cmd := exec.Command("sh", "-c", "printf '%s' "+ShellQuote(s))
		cmd.Dir = dir
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("%q: %v", s, err)
		}
		if string(out) != s {
			t.Errorf("round trip %q → %q", s, out)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "PWNED")); err == nil {
		t.Fatal("quoting allowed command execution")
	}
}

func TestExecShCmdNothingExpandsOnHost(t *testing.T) {
	dir, r := fakeDocker(t)
	// The script must see FAKE_IN_CONTAINER (set by the fake docker exec), i.e.
	// it is expanded by the "container" shell, not the host shell.
	out, err := r.RunOutput(ExecShCmd("c1", `echo "${FAKE_IN_CONTAINER:-host} $(echo sub)"`), "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "1 sub" {
		t.Fatalf("out = %q", out)
	}
	// A hostile container ID reaches docker verbatim as one argument.
	id := `id with 'quote' $(x)`
	if _, err := r.RunOutput(ExecShCmd(id, "true"), ""); err != nil {
		t.Fatal(err)
	}
	ids := execIDs(t, dir)
	if ids[len(ids)-1] != id {
		t.Fatalf("container id arrived as %q", ids[len(ids)-1])
	}
}

func TestExecScriptSecrets(t *testing.T) {
	_, r := fakeDocker(t)
	for _, secret := range hostile {
		if strings.Contains(secret, "\n") {
			continue
		}
		hc := ExecScript("c", `printf '[%s|%s]' "$A" "$B"`, Secret{"A", secret}, Secret{"B", "second"})
		if secret != "" && len(secret) > 3 && strings.Contains(hc.Cmd, secret) {
			t.Fatalf("secret %q leaked into %q", secret, hc.Cmd)
		}
		out, err := r.RunOutput(hc.Cmd, hc.Input)
		if err != nil {
			t.Fatal(err)
		}
		if out != "["+secret+"|second]" {
			t.Errorf("secret %q arrived as %q", secret, out)
		}
	}
}

func TestExecScriptWithoutSecretsHasNoStdin(t *testing.T) {
	hc := ExecScript("c", "true")
	if hc.Input != "" || strings.Contains(hc.Cmd, " -i ") {
		t.Fatalf("unexpected interactive command: %+v", hc)
	}
}

func TestSecretInputDropsNewlines(t *testing.T) {
	got := secretInput([]Secret{{"A", "a\nb\r"}, {"B", ""}})
	if got != "ab\n\n" {
		t.Fatalf("got %q", got)
	}
}

// pidAlive reports whether pid still exists (and is not a zombie we reaped).
func pidAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

func TestStoppableKillsJobOnStop(t *testing.T) {
	_, r := fakeDocker(t)
	pidFile := filepath.Join(t.TempDir(), "pid")
	hc := ExecStreamScript("c", `echo $$ > `+shellQuote(pidFile)+`; echo ready; exec sleep 300`)
	ch, stop, err := Stream(r, hc)
	if err != nil {
		t.Fatal(err)
	}
	if l := <-ch; l != "ready" {
		t.Fatalf("line = %q", l)
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	if !pidAlive(pid) {
		t.Fatal("job not running")
	}
	stop()
	deadline := time.Now().Add(5 * time.Second)
	for pidAlive(pid) {
		if time.Now().After(deadline) {
			t.Fatal("job still alive after stop")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestStoppableKeepsExitStatus(t *testing.T) {
	_, r := fakeDocker(t)
	hc := ExecStreamScript("c", "echo out; exit 5")
	p, err := r.StartCommand(hc.Cmd, hc.Input)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(p.Stdout())
	err = p.Wait()
	if string(data) != "out\n" {
		t.Fatalf("stdout = %q", data)
	}
	var ee *exec.ExitError
	if err == nil || !errors.As(err, &ee) || ee.ExitCode() != 5 {
		t.Fatalf("want exit 5, got %v", err)
	}
}

func TestStoppableEndsWithoutStop(t *testing.T) {
	_, r := fakeDocker(t)
	ch, stop, err := Stream(r, ExecStreamScript("c", "echo a; echo b"))
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	var lines []string
	timeout := time.After(5 * time.Second)
	for {
		select {
		case l, ok := <-ch:
			if !ok {
				if strings.Join(lines, ",") != "a,b" {
					t.Fatalf("lines = %q", lines)
				}
				return
			}
			lines = append(lines, l)
		case <-timeout:
			t.Fatal("stream did not end on its own")
		}
	}
}

func TestHostStreamScript(t *testing.T) {
	r := connection.NewLocalClient()
	ch, stop, err := Stream(r, HostStreamScript(`echo "${FAKE_IN_CONTAINER:-host}"`))
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if l := <-ch; l != "host" {
		t.Fatalf("line = %q", l)
	}
}

func TestGzipPipe(t *testing.T) {
	r := connection.NewLocalClient()
	cases := []struct {
		producer string
		data     string
		wantErr  bool
	}{
		{"echo hello", "hello\n", false},
		{"echo partial; exit 7", "partial\n", true},
		{"printf 'a\\nb'; false", "a\nb", true},
	}
	for _, c := range cases {
		out, err := r.RunOutput(HostShCmd(gzipPipe(c.producer)), "")
		if (err != nil) != c.wantErr {
			t.Errorf("%q: err = %v", c.producer, err)
		}
		zr, zerr := gzip.NewReader(strings.NewReader(out))
		if zerr != nil {
			t.Fatalf("%q: not gzip: %v", c.producer, zerr)
		}
		data, _ := io.ReadAll(zr)
		if string(data) != c.data {
			t.Errorf("%q: data = %q", c.producer, data)
		}
	}
}

func TestCustomCommandScript(t *testing.T) {
	_, r := fakeDocker(t)
	work := filepath.Join(t.TempDir(), "my app")
	if err := os.Mkdir(work, 0o755); err != nil {
		t.Fatal(err)
	}
	ch, stop, err := ExecCustomCommand(r, "c", work, `pwd; echo "$FAKE_IN_CONTAINER"`)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	got := []string{<-ch, <-ch}
	real, _ := filepath.EvalSymlinks(work)
	if (got[0] != work && got[0] != real) || got[1] != "1" {
		t.Fatalf("got %q", got)
	}
	// A missing work dir falls back to the current directory.
	if s := CustomCommandScript("", "ls"); s != "ls" {
		t.Fatalf("no workdir: %q", s)
	}
	if s := CustomCommandScript("/no such", "ls"); s != "cd '/no such' 2>/dev/null; ls" {
		t.Fatalf("workdir: %q", s)
	}
}

func TestExecArtisanQuotesPathButNotArgs(t *testing.T) {
	_, r := fakeDocker(t)
	root := filepath.Join(t.TempDir(), "app root")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	// A fake "php" that prints its arguments, one per line.
	bin := filepath.Join(t.TempDir(), "bin")
	os.Mkdir(bin, 0o755)
	os.WriteFile(filepath.Join(bin, "php"), []byte("#!/bin/sh\nfor a; do echo \"<$a>\"; done\n"), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	ch, stop, err := ExecArtisan(r, "c", root, `migrate --path="a b" --env=$FAKE_IN_CONTAINER`)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	var got []string
	for l := range ch {
		got = append(got, l)
	}
	want := []string{"<" + root + "/artisan>", "<migrate>", "<--path=a b>", "<--env=1>"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q, want %q", got, want)
	}
}
