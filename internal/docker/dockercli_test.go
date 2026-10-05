package docker

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexbabintsev/laradok/internal/connection"
)

// fakeCLI installs an executable "mydocker" that records its arguments and
// then behaves like the fake docker.
func fakeCLI(t *testing.T, dir string) string {
	t.Helper()
	bin := filepath.Join(dir, "clibin")
	os.Mkdir(bin, 0o755)
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$FAKE_DIR/cli_calls\"\nexec docker \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "mydocker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return bin
}

func cliCalls(t *testing.T, dir string) []string {
	t.Helper()
	data, _ := os.ReadFile(filepath.Join(dir, "cli_calls"))
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func TestWithDockerCLIDefaultIsIdentity(t *testing.T) {
	r := connection.NewLocalClient()
	for _, cli := range []string{"", " ", "docker"} {
		if got := WithDockerCLI(r, cli); got != Runner(r) {
			t.Errorf("%q wrapped the runner", cli)
		}
	}
	if dockerPrelude("") != "" || dockerPrelude("docker") != "" {
		t.Error("default CLI has a prelude")
	}
}

func TestWithDockerCLIRoutesEveryDockerCall(t *testing.T) {
	dir, base := fakeDocker(t)
	fakeCLI(t, dir)
	writeFixture(t, dir, "ps.out", `{"id":"a1","name":"web","image":"i","state":"running","status":"Up","ports":""}`+"\n")
	r := WithDockerCLI(base, "mydocker")
	if runnerDockerCLI(r) != "mydocker" {
		t.Fatal("CLI not exposed")
	}

	// Direct call.
	if cs, err := ListContainers(r, false); err != nil || len(cs) != 1 {
		t.Fatalf("list: %+v %v", cs, err)
	}
	// docker inside $(…) on the host.
	out, err := r.RunOutput(`echo "$(docker ps --format x)"`, "")
	if err != nil || !strings.Contains(out, "a1") {
		t.Fatalf("nested: %q %v", out, err)
	}
	// Quoted user data mentioning docker inside the container is untouched.
	ch, stop, err := ExecCustomCommand(r, "c", "", `echo "docker ps; docker rm"`)
	if err != nil {
		t.Fatal(err)
	}
	if l := <-ch; l != "docker ps; docker rm" {
		t.Fatalf("container output %q", l)
	}
	stop()

	// (The exec call's script spans several lines of the log.)
	calls := cliCalls(t, dir)
	if len(calls) < 3 || !strings.HasPrefix(calls[0], "ps --format") || !strings.HasPrefix(calls[1], "ps --format x") || !strings.HasPrefix(calls[2], "exec -i c sh -c") {
		t.Fatalf("calls through the custom CLI: %q", calls)
	}
}

func TestWithDockerCLIWrapsDockerPrefix(t *testing.T) {
	// "sudo docker"-style values must not recurse into the function itself.
	if got := dockerPrelude("docker --context prod"); got != `docker() { command docker --context prod "$@"; }; ` {
		t.Fatalf("prelude %q", got)
	}
	if got := dockerPrelude("sudo -n docker"); got != `docker() { sudo -n docker "$@"; }; ` {
		t.Fatalf("prelude %q", got)
	}
	dir, base := fakeDocker(t)
	writeFixture(t, dir, "ps.out", "")
	r := WithDockerCLI(base, "docker --quiet")
	if _, err := ListContainers(r, true); err != nil {
		t.Fatalf("self-referencing CLI: %v", err)
	}
}

func TestWithDockerCLIDockerLogsNestedShell(t *testing.T) {
	dir, base := fakeDocker(t)
	fakeCLI(t, dir)
	script := strings.Replace(fakeDockerScript, "ps) cat", `logs) echo "logs for $4"; exec sleep 300 ;;
ps) cat`, 1)
	os.WriteFile(mustLookDocker(t), []byte(script), 0o755)

	ch, stop, err := TailDockerLogs(WithDockerCLI(base, "mydocker"), "c1")
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if l := <-ch; l != "logs for c1" {
		t.Fatalf("line %q", l)
	}
	if calls := cliCalls(t, dir); len(calls) != 1 || !strings.HasPrefix(calls[0], "logs -f") {
		t.Fatalf("docker logs bypassed the custom CLI: %q", calls)
	}
}

func TestWithDockerCLIAllMethods(t *testing.T) {
	dir, base := fakeDocker(t)
	fakeCLI(t, dir)
	r := WithDockerCLI(base, "mydocker")
	if _, err := r.RunCommand("docker exec c true"); err != nil {
		t.Fatal(err)
	}
	p, err := r.StartCommand("docker exec c echo hi", "")
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := io.ReadAll(p.Stdout()); string(data) != "hi\n" || p.Wait() != nil {
		t.Fatalf("start: %q", data)
	}
	out, in, stop, err := r.InteractiveCommand("docker exec -i c cat")
	if err != nil {
		t.Fatal(err)
	}
	in <- "x"
	if l := <-out; l != "x" {
		t.Fatalf("interactive %q", l)
	}
	stop()
	if n := len(cliCalls(t, dir)); n != 3 {
		t.Fatalf("calls = %d", n)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestListContainersRunningOnly(t *testing.T) {
	dir, r := fakeDocker(t)
	// The fake ps prints the fixture regardless; check the flag instead.
	script := strings.Replace(fakeDockerScript, "ps) cat \"$FAKE_DIR/ps.out\" ;;", `ps) printf '%s\n' "$*" > "$FAKE_DIR/ps_args"; cat "$FAKE_DIR/ps.out" ;;`, 1)
	os.WriteFile(mustLookDocker(t), []byte(script), 0o755)
	writeFixture(t, dir, "ps.out", "")
	ListContainers(r, false)
	if a, _ := os.ReadFile(filepath.Join(dir, "ps_args")); strings.Contains(string(a), "-a") {
		t.Fatalf("running-only listing used -a: %s", a)
	}
	ListContainers(r, true)
	if a, _ := os.ReadFile(filepath.Join(dir, "ps_args")); !strings.HasPrefix(string(a), "-a ") {
		t.Fatalf("full listing lacks -a: %s", a)
	}
}

func TestSetDownloadsDir(t *testing.T) {
	old := downloadsDir.Load()
	defer downloadsDir.Store(old)
	SetDownloadsDir("/tmp/custom-dl")
	if d, _ := downloadsDirFunc(); d != "/tmp/custom-dl" {
		t.Fatalf("dir %q", d)
	}
	SetDownloadsDir("")
	if d, _ := downloadsDirFunc(); !strings.HasSuffix(d, "Downloads") {
		t.Fatalf("default dir %q", d)
	}
}
