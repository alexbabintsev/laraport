package docker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeBin installs an executable script named name first in PATH.
func fakeBin(t *testing.T, name, script string) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestListArtisanCommandsFake(t *testing.T) {
	_, r := fakeDocker(t)
	fakeBin(t, "php", `[ "$2" = list ] || exit 9; printf 'about    Info\nmigrate  Run migrations\n'`)
	cmds, err := ListArtisanCommands(r, "c", "/app")
	if err != nil || len(cmds) != 2 || cmds[1].Name != "migrate" {
		t.Fatalf("cmds %+v err %v", cmds, err)
	}
	fakeBin(t, "php", `echo "PHP Fatal" >&2; exit 255`)
	if _, err := ListArtisanCommands(r, "c", "/app"); err == nil || !strings.Contains(err.Error(), "PHP Fatal") {
		t.Fatalf("err = %v", err)
	}
}

func TestListComposerCommandsSystemBinary(t *testing.T) {
	_, r := fakeDocker(t)
	root := t.TempDir()
	fakeBin(t, "composer", `printf 'Available commands:\n  install  Installs\n'`)
	cmds, bin, err := ListComposerCommands(r, "c", root)
	if err != nil || bin != "composer" || len(cmds) != 1 || cmds[0].Name != "install" {
		t.Fatalf("cmds %+v bin %q err %v", cmds, bin, err)
	}
}

func TestResolveComposerPhar(t *testing.T) {
	_, r := fakeDocker(t)
	// Hide any real composer on this machine.
	t.Setenv("PATH", filepath.Dir(mustLookDocker(t))+string(os.PathListSeparator)+"/usr/bin:/bin")
	root := filepath.Join(t.TempDir(), "my app")
	os.MkdirAll(root, 0o755)
	os.WriteFile(filepath.Join(root, "composer.phar"), nil, 0o644)

	bin, err := resolveComposer(r, "c", root)
	if err != nil || bin != "php "+shellQuote(root+"/composer.phar") {
		t.Fatalf("bin %q err %v", bin, err)
	}
}

func TestResolveComposerDownloadFailure(t *testing.T) {
	_, r := fakeDocker(t)
	fake := filepath.Dir(mustLookDocker(t))
	t.Setenv("PATH", fake+string(os.PathListSeparator)+"/usr/bin:/bin")
	// A php that fails like a signature mismatch would.
	fakeBin(t, "php", `echo "composer installer signature mismatch" >&2; exit 1`)
	root := t.TempDir()
	_, err := resolveComposer(r, "c", root)
	if err == nil || !strings.Contains(err.Error(), "signature mismatch") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "composer-setup.php")); err == nil {
		t.Fatal("installer left behind")
	}
}

func mustLookDocker(t *testing.T) string {
	t.Helper()
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		p := filepath.Join(dir, "docker")
		if data, err := os.ReadFile(p); err == nil && strings.Contains(string(data), "fake docker") {
			return p
		}
	}
	t.Fatal("fake docker not in PATH")
	return ""
}

func TestListNpmCommandsFake(t *testing.T) {
	_, r := fakeDocker(t)
	fakeBin(t, "npm", `printf 'Scripts available in x via %s:\n  dev\n    vite\n' '`+"`npm run-script`"+`'`)
	cmds, err := ListNpmCommands(r, "c", t.TempDir())
	if err != nil || len(cmds) != 1 || cmds[0].Desc != "vite" {
		t.Fatalf("cmds %+v err %v", cmds, err)
	}
}

func TestTailDockerLogsFake(t *testing.T) {
	dir, r := fakeDocker(t)
	// Extend the fake docker with "logs".
	script := strings.Replace(fakeDockerScript, "ps) cat", `logs) echo "logs for $4"; exec sleep 300 ;;
ps) cat`, 1)
	os.WriteFile(mustLookDocker(t), []byte(script), 0o755)
	_ = dir

	ch, stop, err := TailDockerLogs(r, "my container")
	if err != nil {
		t.Fatal(err)
	}
	if l := <-ch; l != "logs for my container" {
		t.Fatalf("line %q", l)
	}
	done := make(chan struct{})
	go func() { stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stop blocked")
	}
}

func TestTailLaravelLogFallbackForEmptyFile(t *testing.T) {
	_, r := fakeDocker(t)
	root := t.TempDir()
	logs := filepath.Join(root, "storage", "logs")
	os.MkdirAll(logs, 0o755)
	os.WriteFile(filepath.Join(logs, "laravel.log"), nil, 0o644)

	ch, stop, total, top, err := TailLaravelLog(r, "c", root)
	if err != nil || total != 0 || top != 0 {
		t.Fatalf("total %d top %d err %v", total, top, err)
	}
	defer stop()
	f, _ := os.OpenFile(filepath.Join(logs, "laravel.log"), os.O_APPEND|os.O_WRONLY, 0)
	time.Sleep(300 * time.Millisecond)
	f.WriteString("new entry\n")
	f.Close()
	select {
	case l := <-ch:
		if l != "new entry" {
			t.Fatalf("line %q", l)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("appended line not followed")
	}
}

func TestStorageSizeFake(t *testing.T) {
	_, r := fakeDocker(t)
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "storage"), 0o755)
	os.WriteFile(filepath.Join(root, "storage", "f"), make([]byte, 10000), 0o644)
	size, err := StorageSize(r, "c", root)
	if err != nil || size == "" || size == "?" {
		t.Fatalf("size %q err %v", size, err)
	}
	if size, _ := StorageSize(r, "c", filepath.Join(root, "missing")); size != "?" {
		t.Fatalf("missing dir size %q", size)
	}
}

func TestDumpSQLiteDatabaseFake(t *testing.T) {
	_, r := fakeDocker(t)
	dir := useTempDownloads(t)
	fakeBin(t, "sqlite3", `[ "$2" = .dump ] || exit 9; echo "CREATE TABLE t(x); -- $1"`)
	ch, stop, err := DumpSQLiteDatabase(r, "c", "/app/database/my db.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	saved := savedPath(t, drain(t, ch, 10*time.Second))
	if filepath.Dir(saved) != dir || !strings.HasPrefix(filepath.Base(saved), "my_db_") {
		t.Fatalf("saved %q", saved)
	}
	if sql := gunzipString(t, saved); sql != "CREATE TABLE t(x); -- /app/database/my db.sqlite\n" {
		t.Fatalf("sql %q", sql)
	}
}

func TestSQLiteExecCmdFake(t *testing.T) {
	_, r := fakeDocker(t)
	fakeBin(t, "sqlite3", `for a; do echo "<$a>"; done`)
	ch, stop, err := Stream(r, SQLiteExecCmd("c", "/d/a b.db", "SELECT 'x'"))
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	got := strings.Join(drain(t, ch, 5*time.Second), "")
	if got != "<-header><-column></d/a b.db><SELECT 'x'>" {
		t.Fatalf("args %q", got)
	}
}
