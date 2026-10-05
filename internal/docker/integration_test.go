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

// shells are the base images whose /bin/sh differs: busybox ash and dash.
var shells = []string{"alpine:3.20", "debian:bookworm-slim"}

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
		for _, f := range files {
			got[filepath.Base(f.Path)] = f
		}
		for i, n := range names {
			f, ok := got[n]
			if !ok {
				t.Fatalf("missing %q in %v", n, files)
			}
			if f.Lines != i+1 {
				t.Errorf("%q: lines = %d, want %d", n, f.Lines, i+1)
			}
			if f.Size == 0 {
				t.Errorf("%q: size 0", n)
			}
			// The path must reach the container verbatim for these, too.
			cnt, err := CountFileLines(r, id, f.Path)
			if err != nil || cnt != i+1 {
				t.Errorf("CountFileLines(%q) = %d, %v", n, cnt, err)
			}
			chunk, _, err := LoadLogChunk(r, id, f.Path, 1)
			if err != nil || len(chunk) != i+1 {
				t.Errorf("LoadLogChunk(%q) = %d lines, %v", n, len(chunk), err)
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
		dexec(t, id, fmt.Sprintf("i=1; while [ $i -le 1500 ]; do echo line$i; i=$((i+1)); done > %s", shellQuote(p)))

		ch, stop, total, top, err := TailLogFile(r, id, p)
		if err != nil {
			t.Fatal(err)
		}
		if total != 1500 || top != 501 {
			t.Fatalf("total=%d top=%d", total, top)
		}
		for i := 501; i <= 1500; i++ {
			if l := <-ch; l != fmt.Sprintf("line%d", i) {
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
		stop()
		waitFor(t, 10*time.Second, "tail to exit", func() bool { return processCount(t, id, "tail -n +1501 -f") == 0 })
	})
}

func TestIntegrationSecretsStayOffCommandLines(t *testing.T) {
	withShells(t, func(t *testing.T, r Runner, id string) {
		secret := `pa'ss "w$x\ord`
		hc := ExecStreamScript(id, `echo "[$MY_SECRET]"; sleep 30`, Secret{Name: "MY_SECRET", Value: secret})
		if strings.Contains(hc.Cmd, "pa'ss") || strings.Contains(hc.Cmd, "w$x") {
			t.Fatalf("secret leaked into the command line: %s", hc.Cmd)
		}
		ch, stop, err := Stream(r, hc)
		if err != nil {
			t.Fatal(err)
		}
		defer stop()
		if l := <-ch; l != "["+secret+"]" {
			t.Fatalf("secret arrived as %q", l)
		}
		// No process in the container carries the secret in its argv.
		if n := processCount(t, id, "w$x"); n != 0 {
			t.Fatalf("%d processes expose the secret in argv", n)
		}
	})
}

func TestIntegrationGzipPipeKeepsProducerStatus(t *testing.T) {
	withShells(t, func(t *testing.T, r Runner, id string) {
		dexec(t, id, "command -v gzip")
		out, err := r.RunOutput(ExecShCmd(id, gzipPipe("echo partial; exit 7")), "")
		if err == nil || !strings.Contains(err.Error(), "7") {
			t.Fatalf("want exit status 7, got %v", err)
		}
		zr, zerr := gzip.NewReader(strings.NewReader(out))
		if zerr != nil {
			t.Fatalf("output is not gzip: %v", zerr)
		}
		data, _ := io.ReadAll(zr)
		if string(data) != "partial\n" {
			t.Fatalf("data = %q", data)
		}
		if _, err := r.RunOutput(ExecShCmd(id, gzipPipe("echo ok")), ""); err != nil {
			t.Fatalf("successful producer reported %v", err)
		}
	})
}

func TestIntegrationDownloadPath(t *testing.T) {
	withShellsSerial(t, func(t *testing.T, r Runner, id string) {
		dir := useTempDownloads(t)
		dexec(t, id, `mkdir -p "/data/my dir/sub" && echo hello > "/data/my dir/a.txt" && `+
			`head -c 3000000 /dev/urandom > "/data/my dir/sub/blob.bin"`)

		ch, stop, err := DownloadPath(r, id, "app/1", "/data/my dir")
		if err != nil {
			t.Fatal(err)
		}
		defer stop()
		saved := savedPath(t, drain(t, ch, 60*time.Second))
		if filepath.Dir(saved) != dir || !strings.HasPrefix(filepath.Base(saved), "app_1_my_dir_") {
			t.Fatalf("saved as %s", saved)
		}
		st, err := os.Stat(saved)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != 0o600 {
			t.Errorf("mode = %v, want 0600", st.Mode().Perm())
		}
		entries := tarEntries(t, saved)
		if entries["my dir/a.txt"] != 6 || entries["my dir/sub/blob.bin"] != 3000000 {
			t.Fatalf("archive entries = %v", entries)
		}
		assertNoPartFiles(t, dir)
	})
}

func TestIntegrationDownloadCancel(t *testing.T) {
	withShellsSerial(t, func(t *testing.T, r Runner, id string) {
		dir := useTempDownloads(t)
		// An endless producer: only cancellation can end it.
		hc := ExecStreamScript(id, "cat /dev/urandom")
		ch, stop, err := download(r, hc, "start", "endless.bin", nil)
		if err != nil {
			t.Fatal(err)
		}
		<-ch // start line
		waitFor(t, 10*time.Second, "data to flow", func() bool {
			m, _ := filepath.Glob(filepath.Join(dir, ".laradok-*.part"))
			if len(m) == 0 {
				return false
			}
			st, err := os.Stat(m[0])
			return err == nil && st.Size() > 0
		})
		stop()
		assertNoPartFiles(t, dir)
		if m, _ := filepath.Glob(filepath.Join(dir, "endless*")); len(m) != 0 {
			t.Fatalf("cancelled download left %v", m)
		}
		waitFor(t, 10*time.Second, "cat to be killed", func() bool { return processCount(t, id, "cat /dev/urandom") == 0 })
	})
}

func TestIntegrationDownloadFailureLeavesNothing(t *testing.T) {
	withShellsSerial(t, func(t *testing.T, r Runner, id string) {
		dir := useTempDownloads(t)
		ch, stop, err := DownloadPath(r, id, "c", "/does/not/exist")
		if err != nil {
			t.Fatal(err)
		}
		defer stop()
		lines := drain(t, ch, 30*time.Second)
		last := lines[len(lines)-1]
		if !strings.HasPrefix(last, "ERROR") {
			t.Fatalf("want ERROR, got %q", lines)
		}
		if m, _ := filepath.Glob(filepath.Join(dir, "*")); len(m) != 0 {
			t.Fatalf("failed download left %v", m)
		}
		if m, _ := filepath.Glob(filepath.Join(dir, ".*")); len(m) != 0 {
			t.Fatalf("failed download left %v", m)
		}
	})
}

func TestIntegrationStorageDownloadWhileLogsChange(t *testing.T) {
	requireIntegration(t)
	// GNU tar (Debian) exits 1 for "file changed as we read it".
	r := connection.NewLocalClient()
	id := startContainer(t, "debian:bookworm-slim", "--", "sleep", "3600")
	dir := useTempDownloads(t)
	dexec(t, id, "mkdir -p /var/www/html/storage/logs && head -c 20000000 /dev/zero > /var/www/html/storage/logs/laravel.log")
	// Keep growing the log while it is archived.
	exec.Command("docker", "exec", "-d", id, "sh", "-c", "while :; do echo more >> /var/www/html/storage/logs/laravel.log; done").Run() //nolint:errcheck

	ch, stop, err := DownloadStorage(r, id, "web", "")
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	saved := savedPath(t, drain(t, ch, 60*time.Second))
	if _, ok := tarEntries(t, saved)["storage/logs/laravel.log"]; !ok {
		t.Fatal("laravel.log missing from archive")
	}
	_ = dir
}

func TestIntegrationDetectCapabilities(t *testing.T) {
	withShells(t, func(t *testing.T, r Runner, id string) {
		caps, err := DetectCapabilities(r, id, "")
		if err != nil {
			t.Fatal(err)
		}
		if caps.HasLaravel || caps.HasPHP {
			t.Fatalf("plain image detected as Laravel: %+v", caps)
		}
		dexec(t, id, "mkdir -p '/srv/my app' && touch '/srv/my app/artisan'")
		caps, err = DetectCapabilities(r, id, "/srv/my app/")
		if err != nil {
			t.Fatal(err)
		}
		if !caps.HasLaravel || caps.LaravelRoot != "/srv/my app" {
			t.Fatalf("caps = %+v", caps)
		}
		dexec(t, id, "mkdir -p /app && touch /app/artisan")
		caps, _ = DetectCapabilities(r, id, "")
		if caps.LaravelRoot != "/app" {
			t.Fatalf("LaravelRoot = %q, want /app", caps.LaravelRoot)
		}
	})
}

func TestIntegrationContainerListAndInspect(t *testing.T) {
	requireIntegration(t)
	r := connection.NewLocalClient()
	id := startContainer(t, "alpine:3.20", "--name", fmt.Sprintf("laradok-test-%d", time.Now().UnixNano()), "-p", "127.0.0.1::80", "--", "sleep", "3600")

	cs, err := ListContainers(r)
	if err != nil {
		t.Fatal(err)
	}
	var found *Container
	for i := range cs {
		if strings.HasPrefix(id, cs[i].ID) {
			found = &cs[i]
		}
	}
	if found == nil || found.State != "running" || !strings.HasPrefix(found.Name, "laradok-test-") || !strings.Contains(found.Ports, "->80/tcp") {
		t.Fatalf("container not listed correctly: %+v", found)
	}
	info, err := InspectContainer(r, id)
	if err != nil || info.Image != "alpine:3.20" {
		t.Fatalf("inspect: %+v %v", info, err)
	}
	if _, err := SampleContainerStats(r, id); err != nil {
		t.Fatalf("stats: %v", err)
	}
}

func TestIntegrationListDir(t *testing.T) {
	withShells(t, func(t *testing.T, r Runner, id string) {
		dexec(t, id, `mkdir -p "/d/sub dir" && printf abc > "/d/f'1" && touch /d/.hidden`)
		entries, err := ListDir(r, id, "/d/")
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, e := range entries {
			names = append(names, e.Name)
		}
		if strings.Join(names, ",") != "sub dir,.hidden,f'1" {
			t.Fatalf("entries = %q", names)
		}
		if entries[2].Size != 3 || entries[2].Path != "/d/f'1" || !entries[0].IsDir {
			t.Fatalf("entries = %+v", entries)
		}
	})
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
