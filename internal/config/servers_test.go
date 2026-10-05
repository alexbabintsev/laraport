package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sshServer(name string) Server {
	return Server{Name: name, Type: ServerTypeSSH, Host: "example.com", Port: 22, User: "deploy"}
}

func TestServerValidate(t *testing.T) {
	key := filepath.Join(t.TempDir(), "id")
	os.WriteFile(key, []byte("k"), 0o600)
	cases := []struct {
		srv  Server
		want string // substring of the error; "" = valid
	}{
		{sshServer("prod"), ""},
		{Server{Name: "local", Type: ServerTypeLocal}, ""},
		{Server{Name: " ", Type: ServerTypeLocal}, "name is required"},
		{Server{Name: "x", Type: "ftp"}, "type must be"},
		{Server{Name: "x", Type: ServerTypeSSH, Port: 22, User: "u"}, "host is required"},
		{Server{Name: "x", Type: ServerTypeSSH, Host: "-oProxyCommand=x", Port: 22, User: "u"}, "plain host"},
		{Server{Name: "x", Type: ServerTypeSSH, Host: "user@host", Port: 22, User: "u"}, "plain host"},
		{Server{Name: "x", Type: ServerTypeSSH, Host: "h", Port: 0, User: "u"}, "port"},
		{Server{Name: "x", Type: ServerTypeSSH, Host: "h", Port: 70000, User: "u"}, "port"},
		{Server{Name: "x", Type: ServerTypeSSH, Host: "h", Port: 22}, "user is required"},
		{Server{Name: "x", Type: ServerTypeSSH, Host: "h", Port: 22, User: "a b"}, "user must not"},
		{Server{Name: "x", Type: ServerTypeSSH, Host: "h", Port: 22, User: "u", Key: key}, ""},
		{Server{Name: "x", Type: ServerTypeSSH, Host: "h", Port: 22, User: "u", Key: key + ".missing"}, "not found"},
		{Server{Name: "x", Type: ServerTypeSSH, Host: "h", Port: 22, User: "u", Key: filepath.Dir(key)}, "directory"},
		{Server{Name: "x", Type: ServerTypeLocal, DockerCmd: "sudo\ndocker"}, "single line"},
		{Server{Name: "x", Type: ServerTypeLocal, RootPath: "app"}, "absolute"},
	}
	for _, c := range cases {
		err := c.srv.Validate()
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%+v: unexpected error %v", c.srv, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%+v: error %v, want %q", c.srv, err, c.want)
		}
	}
}

func TestAddServerPersistsImplicitLocal(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	cfg, _ := Load(p) // no file: implicit Local
	if err := cfg.AddServer(Server{Name: "prod", Type: ServerTypeSSH, Host: " example.com ", User: "deploy"}); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(p); err != nil {
		t.Fatal(err)
	}
	again, _ := Load(p)
	if len(again.Servers) != 2 || again.Servers[0].Name != "Local" || again.Servers[1].Host != "example.com" || again.Servers[1].Port != 22 {
		t.Fatalf("servers = %+v", again.Servers)
	}
	if err := cfg.AddServer(sshServer("prod")); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate: %v", err)
	}
	if err := cfg.AddServer(Server{Name: "bad", Type: ServerTypeSSH}); err == nil {
		t.Fatal("invalid server added")
	}
}

func TestUpdateServerKeepsContainersAndPassphrase(t *testing.T) {
	cfg := &Config{Servers: []Server{{
		Name: "prod", Type: ServerTypeSSH, Host: "h", Port: 22, User: "u", Key: "/k", Passphrase: "pp",
		Containers: []ContainerConfig{{Name: "web", Favorite: true}},
	}, {Name: "other", Type: ServerTypeLocal}}}
	key := filepath.Join(t.TempDir(), "k")
	os.WriteFile(key, nil, 0o600)
	cfg.Servers[0].Key = key

	upd := sshServer("production")
	upd.Key = key
	upd.DockerCmd = " sudo -n docker "
	if err := cfg.UpdateServer("prod", upd); err != nil {
		t.Fatal(err)
	}
	s := cfg.Servers[0]
	if s.Name != "production" || len(s.Containers) != 1 || s.Passphrase != "pp" || s.DockerCmd != "sudo -n docker" {
		t.Fatalf("updated = %+v", s)
	}
	// Changing the key drops the passphrase that belonged to the old key.
	upd.Key = ""
	cfg.UpdateServer("production", upd)
	if cfg.Servers[0].Passphrase != "" {
		t.Fatal("passphrase kept for a different key")
	}
	if err := cfg.UpdateServer("production", Server{Name: "other", Type: ServerTypeLocal}); err == nil {
		t.Fatal("rename onto an existing name succeeded")
	}
	if err := cfg.UpdateServer("missing", sshServer("x")); err == nil {
		t.Fatal("update of a missing server succeeded")
	}
	// Switching to local clears the SSH fields.
	cfg.UpdateServer("production", Server{Name: "production", Type: ServerTypeLocal, Host: "h", User: "u"})
	if s := cfg.Servers[0]; s.Host != "" || s.User != "" || s.Port != 0 {
		t.Fatalf("local server kept ssh fields: %+v", s)
	}
}

func TestDeleteServer(t *testing.T) {
	cfg := &Config{Servers: []Server{sshServer("a"), sshServer("b")}}
	if err := cfg.DeleteServer("a"); err != nil || len(cfg.Servers) != 1 || cfg.Servers[0].Name != "b" {
		t.Fatalf("after delete: %+v %v", cfg.Servers, err)
	}
	if err := cfg.DeleteServer("missing"); err == nil {
		t.Fatal("deleted a missing server")
	}
	cfg.DeleteServer("b")
	if len(cfg.Servers) != 1 || !cfg.Servers[0].IsImplicit() {
		t.Fatalf("last delete should restore implicit Local: %+v", cfg.Servers)
	}
	p := filepath.Join(t.TempDir(), "c.yaml")
	cfg.Save(p)
	if data, _ := os.ReadFile(p); strings.Contains(string(data), "servers") {
		t.Fatalf("implicit Local written:\n%s", data)
	}
	if _, ok := cfg.FindServer("Local"); !ok {
		t.Fatal("FindServer")
	}
}

func TestSaveBacksUpOnce(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	original := "# my comments\nservers:\n  - name: x\n    type: local\n"
	os.WriteFile(p, []byte(original), 0o644)
	cfg, _ := Load(p)
	cfg.Save(p)
	cfg.Servers[0].Name = "y"
	cfg.Save(p)
	bak, err := os.ReadFile(p + ".bak")
	if err != nil || string(bak) != original {
		t.Fatalf("backup = %q %v", bak, err)
	}
	if st, _ := os.Stat(p + ".bak"); st.Mode().Perm() != 0o600 {
		t.Fatalf("backup mode %v", st.Mode().Perm())
	}
	// No file yet → no backup.
	fresh := filepath.Join(t.TempDir(), "new.yaml")
	(&Config{}).Save(fresh)
	if _, err := os.Stat(fresh + ".bak"); err == nil {
		t.Fatal("backup created for a new file")
	}
}

func TestSettingsDefaultsAndValidate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var st Settings
	if st.Downloads() != filepath.Join(home, "Downloads") || st.StrictHostKeys() || st.StatsEvery() != 2*time.Second || st.SQLHistoryLimit() != 200 {
		t.Fatalf("defaults: %+v", st)
	}
	st = Settings{DownloadsDir: "~/dumps", HostKeyCheck: HostKeyStrict, StatsInterval: 5, SQLHistorySize: 50}
	if st.Downloads() != filepath.Join(home, "dumps") || !st.StrictHostKeys() || st.StatsEvery() != 5*time.Second || st.SQLHistoryLimit() != 50 {
		t.Fatalf("custom: %+v", st)
	}
	if (Settings{NoSQLHistory: true, SQLHistorySize: 50}).SQLHistoryLimit() != 0 {
		t.Fatal("disabled history has a limit")
	}
	file := filepath.Join(home, "f")
	os.WriteFile(file, nil, 0o600)
	bad := []Settings{
		{HostKeyCheck: "maybe"},
		{StatsInterval: 61},
		{StatsInterval: -1},
		{SQLHistorySize: 10001},
		{DownloadsDir: "relative/dir"},
		{DownloadsDir: file},
	}
	for _, b := range bad {
		if b.Validate() == nil {
			t.Errorf("%+v accepted", b)
		}
	}
	if err := (Settings{DownloadsDir: "~/x", HostKeyCheck: HostKeyAcceptNew, StatsInterval: 1}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	cfg, _ := Load(p)
	cfg.Settings = Settings{HostKeyCheck: HostKeyStrict, WrapLogs: true, NoSQLHistory: true}
	cfg.Save(p)
	data, _ := os.ReadFile(p)
	if !strings.Contains(string(data), "host_key_check: strict") || strings.Contains(string(data), "stats_interval") {
		t.Fatalf("saved:\n%s", data)
	}
	again, _ := Load(p)
	if again.Settings != cfg.Settings {
		t.Fatalf("round trip: %+v", again.Settings)
	}
	// Default settings write no settings block.
	cfg.Settings = Settings{}
	cfg.Save(p)
	if data, _ := os.ReadFile(p); strings.Contains(string(data), "settings") {
		t.Fatalf("empty settings written:\n%s", data)
	}
}

func TestSQLHistoryLimitAndClear(t *testing.T) {
	p := useTempHistory(t)
	SaveSQLHistory("k", []string{"a", "b", "c"}, 2)
	if h := LoadSQLHistory("k"); strings.Join(h, ",") != "b,c" {
		t.Fatalf("history %q", h)
	}
	SaveSQLHistory("k2", []string{"x"}, 0) // disabled: nothing stored
	if LoadSQLHistory("k2") != nil {
		t.Fatal("stored with limit 0")
	}
	if err := ClearSQLHistory(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("history file not removed")
	}
	if err := ClearSQLHistory(); err != nil {
		t.Fatal("clearing twice failed")
	}
}
