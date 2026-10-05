package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"gopkg.in/yaml.v3"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadDefaultsAndExpansion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	p := writeConfig(t, `
servers:
  - name: prod
    host: example.com
    user: root
    key: ~/.ssh/id_ed25519
    type: ssh
  - name: other
    key: ~bob/.ssh/key
    port: 2222
  - name: tilde
    key: "~"
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Servers[0].Key; got != filepath.Join(home, ".ssh/id_ed25519") {
		t.Errorf("key = %q", got)
	}
	if cfg.Servers[0].Port != 22 || cfg.Servers[1].Port != 2222 {
		t.Errorf("ports = %d %d", cfg.Servers[0].Port, cfg.Servers[1].Port)
	}
	if got := cfg.Servers[1].Key; got != "~bob/.ssh/key" {
		t.Errorf("~user path mangled: %q", got)
	}
	if got := cfg.Servers[2].Key; got != home {
		t.Errorf("~ = %q", got)
	}
}

func TestLoadMissingFileAddsLocal(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Servers) != 1 || cfg.Servers[0].Type != ServerTypeLocal || !cfg.Servers[0].implicit {
		t.Fatalf("servers = %+v", cfg.Servers)
	}
}

func TestLoadErrors(t *testing.T) {
	if _, err := Load(writeConfig(t, "servers: [unclosed")); err == nil || !strings.Contains(err.Error(), "parsing config") {
		t.Fatalf("err = %v", err)
	}
	dir := t.TempDir()
	if _, err := Load(dir); err == nil {
		t.Fatal("want error reading a directory")
	}
}

func TestFindContainerConfig(t *testing.T) {
	s := Server{Containers: []ContainerConfig{
		{Name: "app-*", DisplayName: "glob1"},
		{Name: "app-*-web", DisplayName: "glob2"},
		{Name: "app-1-web", DisplayName: "exact"},
		{Name: "[bad", DisplayName: "invalid pattern"},
	}}
	cases := map[string]string{
		"app-1-web": "exact", // exact beats earlier globs
		"app-2-web": "glob1", // first glob wins
		"app-x":     "glob1",
	}
	for name, want := range cases {
		cc, ok := s.FindContainerConfig(name)
		if !ok || cc.DisplayName != want {
			t.Errorf("%s → %+v %v", name, cc, ok)
		}
	}
	if _, ok := s.FindContainerConfig("db"); ok {
		t.Error("db should not match")
	}
}

func TestSaveRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	p := writeConfig(t, `
commands:
  - name: Cache
    commands:
      - label: clear
        cmd: php artisan cache:clear
servers:
  - name: prod
    host: h
    key: ~/.ssh/k
    passphrase: secret
    type: ssh
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.UpsertContainerConfig("prod", ContainerConfig{Name: "web", DisplayName: "Web", Favorite: true}) {
		t.Fatal("upsert failed")
	}
	if err := cfg.Save(p); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", st.Mode().Perm())
	}
	data, _ := os.ReadFile(p)
	if !strings.Contains(string(data), "key: ~/.ssh/k") {
		t.Errorf("key not re-collapsed:\n%s", data)
	}
	// In-memory key stays expanded.
	if cfg.Servers[0].Key != filepath.Join(home, ".ssh/k") {
		t.Errorf("in-memory key = %q", cfg.Servers[0].Key)
	}
	again, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	cc, ok := again.Servers[0].FindContainerConfig("web")
	if !ok || cc.DisplayName != "Web" || !cc.Favorite || again.Commands[0].Commands[0].Cmd != "php artisan cache:clear" {
		t.Fatalf("round trip lost data: %+v", again)
	}
	// Only the config and its one-time backup; no temp files left behind.
	entries, _ := os.ReadDir(filepath.Dir(p))
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if strings.Join(names, ",") != "config.yaml,config.yaml.bak" {
		t.Errorf("dir entries: %q", names)
	}
}

func TestSaveSkipsImplicitLocalServer(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "config.yaml")
	cfg, _ := Load(p)
	if err := cfg.Save(p); err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	data, _ := os.ReadFile(p)
	yaml.Unmarshal(data, &raw)
	if raw["servers"] != nil {
		t.Fatalf("implicit server written:\n%s", data)
	}
	// Once it carries container settings it must be persisted.
	cfg.UpsertContainerConfig("Local", ContainerConfig{Name: "web", Hidden: true})
	if err := cfg.Save(p); err != nil {
		t.Fatal(err)
	}
	again, _ := Load(p)
	if len(again.Servers) != 1 || again.Servers[0].Type != ServerTypeLocal || len(again.Servers[0].Containers) != 1 {
		t.Fatalf("servers = %+v", again.Servers)
	}
}

func TestSaveFollowsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "dotfiles", "laraport.yaml")
	os.MkdirAll(filepath.Dir(target), 0o755)
	os.WriteFile(target, []byte("servers: []\n"), 0o644)
	link := filepath.Join(dir, "config.yaml")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	cfg, _ := Load(link)
	cfg.Servers = append(cfg.Servers, Server{Name: "x", Type: ServerTypeLocal})
	if err := cfg.Save(link); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Lstat(link); fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("symlink replaced by a regular file")
	}
	if data, _ := os.ReadFile(target); !strings.Contains(string(data), "name: x") {
		t.Fatalf("target not updated:\n%s", data)
	}
}

func TestUpsertPreservesCommandsAndLogs(t *testing.T) {
	cfg := &Config{Servers: []Server{{Name: "s", Containers: []ContainerConfig{
		{Name: "web", CustomLogs: []string{"/x.log"}, Commands: []CommandGroup{{Name: "g"}}},
	}}}}
	cfg.UpsertContainerConfig("s", ContainerConfig{Name: "web", DisplayName: "W"})
	cc := cfg.Servers[0].Containers[0]
	if cc.DisplayName != "W" || len(cc.CustomLogs) != 1 || len(cc.Commands) != 1 {
		t.Fatalf("cc = %+v", cc)
	}
	if cfg.UpsertContainerConfig("missing", ContainerConfig{Name: "x"}) {
		t.Fatal("upsert into a missing server succeeded")
	}
}

func TestDefaultConfigPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if got := DefaultConfigPath(); got != filepath.Join(home, ".config", "laraport", "config.yaml") {
		t.Fatalf("got %q", got)
	}
}

func useTempHistory(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "laraport", "sql_history.json")
	old := sqlHistoryPathFunc
	sqlHistoryPathFunc = func() string { return p }
	t.Cleanup(func() { sqlHistoryPathFunc = old })
	return p
}

func TestSQLHistory(t *testing.T) {
	p := useTempHistory(t)
	if h := LoadSQLHistory("s/c/db"); h != nil {
		t.Fatalf("empty history = %q", h)
	}
	long := make([]string, 250)
	for i := range long {
		long[i] = fmt.Sprintf("q%d", i)
	}
	if err := SaveSQLHistory("s/c/db", long, DefaultSQLHistorySize); err != nil {
		t.Fatal(err)
	}
	h := LoadSQLHistory("s/c/db")
	if len(h) != DefaultSQLHistorySize || h[0] != "q50" || h[len(h)-1] != "q249" {
		t.Fatalf("history trimmed wrong: %d %q…%q", len(h), h[0], h[len(h)-1])
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", st.Mode().Perm())
	}
	// Corrupt file → empty history, and saving repairs it.
	os.WriteFile(p, []byte("{broken"), 0o600)
	if h := LoadSQLHistory("s/c/db"); h != nil {
		t.Fatalf("corrupt file gave %q", h)
	}
}

func TestSQLHistoryConcurrentSaves(t *testing.T) {
	useTempHistory(t)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			SaveSQLHistory(fmt.Sprintf("key%d", i), []string{"q"}, 10) //nolint:errcheck
		}(i)
	}
	wg.Wait()
	for i := 0; i < 20; i++ {
		if h := LoadSQLHistory(fmt.Sprintf("key%d", i)); len(h) != 1 {
			t.Fatalf("key%d lost (concurrent read-modify-write)", i)
		}
	}
}
