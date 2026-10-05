package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseJumpHost(t *testing.T) {
	cases := []struct {
		in   string
		want Jump
		err  string
	}{
		{"bastion.example.com", Jump{Host: "bastion.example.com", Port: 22}, ""},
		{"ops@bastion:2222", Jump{User: "ops", Host: "bastion", Port: 2222}, ""},
		{" ssh://ops@10.0.0.1 ", Jump{User: "ops", Host: "10.0.0.1", Port: 22}, ""},
		{"[2001:db8::1]:2200", Jump{Host: "2001:db8::1", Port: 2200}, ""},
		{"ops@[2001:db8::1]", Jump{User: "ops", Host: "2001:db8::1", Port: 22}, ""},
		{"2001:db8::1", Jump{Host: "2001:db8::1", Port: 22}, ""},
		{"", Jump{}, "empty"},
		{"a,b", Jump{}, "only one jump host"},
		{"host:0", Jump{}, "port"},
		{"host:99999", Jump{}, "port"},
		{"host:abc", Jump{}, "port"},
		{"-oProxyCommand=x", Jump{}, "invalid jump host"},
		{"-u@host", Jump{}, "invalid jump host user"},
		{"@host", Jump{}, "invalid jump host user"},
		{"a b", Jump{}, "invalid jump host"},
		{"[::1", Jump{}, "invalid jump host"},
		{"[::1]x", Jump{}, "invalid jump host"},
	}
	for _, c := range cases {
		got, err := ParseJumpHost(c.in)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%q: err %v, want %q", c.in, err, c.err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%q → %+v %v, want %+v", c.in, got, err, c.want)
		}
	}
}

func TestJumpString(t *testing.T) {
	cases := map[Jump]string{
		{Host: "b", Port: 22}:                      "b",
		{User: "u", Host: "b", Port: 2222}:         "u@b:2222",
		{User: "u", Host: "2001:db8::1", Port: 22}: "u@[2001:db8::1]",
		{Host: "2001:db8::1", Port: 2200}:          "[2001:db8::1]:2200",
	}
	for j, want := range cases {
		if got := j.String(); got != want {
			t.Errorf("%+v → %q, want %q", j, got, want)
		}
		if back, err := ParseJumpHost(j.String()); err != nil || back.Host != j.Host || back.User != j.User {
			t.Errorf("round trip %q: %+v %v", j.String(), back, err)
		}
	}
}

func TestResolvedJumpDefaults(t *testing.T) {
	srv := Server{Name: "p", Type: ServerTypeSSH, Host: "h", User: "deploy", Key: "/k", Passphrase: "pp", JumpHost: "bastion"}
	j, key, pass, ok, err := srv.ResolvedJump()
	if err != nil || !ok || j.User != "deploy" || key != "/k" || pass != "pp" {
		t.Fatalf("defaults: %+v %q %q %v %v", j, key, pass, ok, err)
	}
	srv.JumpHost, srv.JumpKey = "ops@bastion:2200", "/jk"
	j, key, pass, _, _ = srv.ResolvedJump()
	if j.User != "ops" || j.Port != 2200 || key != "/jk" || pass != "" {
		t.Fatalf("explicit: %+v %q %q", j, key, pass)
	}
	srv.JumpHost = ""
	if _, _, _, ok, err := srv.ResolvedJump(); ok || err != nil {
		t.Fatal("no jump host reported one")
	}
	srv.JumpHost = "a,b"
	if _, _, _, _, err := srv.ResolvedJump(); err == nil {
		t.Fatal("invalid jump host accepted")
	}
}

func TestValidateJump(t *testing.T) {
	key := filepath.Join(t.TempDir(), "jk")
	os.WriteFile(key, nil, 0o600)
	base := Server{Name: "p", Type: ServerTypeSSH, Host: "h", Port: 22, User: "u"}
	cases := []struct {
		jump, jumpKey, want string
	}{
		{"bastion", "", ""},
		{"ops@bastion:2222", key, ""},
		{"a,b", "", "only one jump host"},
		{"bastion", key + ".missing", "jump key file"},
		{"", key, "no jump host"},
	}
	for _, c := range cases {
		s := base
		s.JumpHost, s.JumpKey = c.jump, c.jumpKey
		err := s.Validate()
		if (c.want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.want)) {
			t.Errorf("%+v: err %v, want %q", c, err, c.want)
		}
	}
}

func TestJumpFieldsRoundTripAndLocalClears(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	p := filepath.Join(t.TempDir(), "config.yaml")
	cfg, _ := Load(p)
	os.MkdirAll(filepath.Join(home, ".ssh"), 0o700)
	os.WriteFile(filepath.Join(home, ".ssh", "bastion"), nil, 0o600)
	if err := cfg.AddServer(Server{Name: "p", Type: ServerTypeSSH, Host: "h", User: "u", JumpHost: " ops@b ", JumpKey: "~/.ssh/bastion"}); err != nil {
		t.Fatal(err)
	}
	cfg.Save(p)
	data, _ := os.ReadFile(p)
	if !strings.Contains(string(data), "jump_host: ops@b") || !strings.Contains(string(data), "jump_key: ~/.ssh/bastion") {
		t.Fatalf("saved:\n%s", data)
	}
	again, _ := Load(p)
	if s, _ := again.FindServer("p"); s.JumpKey != filepath.Join(home, ".ssh", "bastion") {
		t.Fatalf("jump key not expanded: %q", s.JumpKey)
	}
	// Switching to local drops the jump settings.
	cfg.UpdateServer("p", Server{Name: "p", Type: ServerTypeLocal, JumpHost: "b"})
	if s, _ := cfg.FindServer("p"); s.JumpHost != "" || s.JumpKey != "" {
		t.Fatalf("local server kept jump settings: %+v", s)
	}
}
