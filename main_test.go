package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexbabintsev/laradok/internal/config"
)

func noTUI(t *testing.T) func(*config.Config, string) error {
	return func(*config.Config, string) error {
		t.Fatal("TUI started")
		return nil
	}
}

func TestVersionAndHelp(t *testing.T) {
	for _, arg := range []string{"-v", "--version", "version"} {
		var out bytes.Buffer
		if code := run([]string{arg}, &out, &out, noTUI(t)); code != 0 || out.String() != "laradok dev\n" {
			t.Errorf("%s: code %d out %q", arg, code, out.String())
		}
	}
	for _, arg := range []string{"-h", "--help", "help"} {
		var out bytes.Buffer
		if code := run([]string{arg}, &out, &out, noTUI(t)); code != 0 || !strings.Contains(out.String(), "Usage:") {
			t.Errorf("%s: code %d out %q", arg, code, out.String())
		}
	}
}

func TestConfigPathResolution(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	envCfg := filepath.Join(t.TempDir(), "env.yaml")
	os.WriteFile(envCfg, []byte("servers:\n  - name: fromenv\n"), 0o600)
	argCfg := filepath.Join(t.TempDir(), "arg.yaml")
	os.WriteFile(argCfg, []byte("servers:\n  - name: fromarg\n"), 0o600)

	var gotPath, gotServer string
	capture := func(cfg *config.Config, p string) error {
		gotPath, gotServer = p, cfg.Servers[0].Name
		return nil
	}
	var out bytes.Buffer

	t.Setenv("LARADOK_CONFIG", "")
	run(nil, &out, &out, capture)
	if gotPath != filepath.Join(home, ".config", "laradok", "config.yaml") || gotServer != "Local" {
		t.Errorf("default: %q %q", gotPath, gotServer)
	}
	t.Setenv("LARADOK_CONFIG", envCfg)
	run(nil, &out, &out, capture)
	if gotPath != envCfg || gotServer != "fromenv" {
		t.Errorf("env: %q %q", gotPath, gotServer)
	}
	run([]string{argCfg}, &out, &out, capture)
	if gotPath != argCfg || gotServer != "fromarg" {
		t.Errorf("arg: %q %q", gotPath, gotServer)
	}
}

func TestErrors(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "bad.yaml")
	os.WriteFile(bad, []byte("servers: [unclosed"), 0o600)
	var out, errOut bytes.Buffer
	if code := run([]string{bad}, &out, &errOut, noTUI(t)); code != 1 || !strings.Contains(errOut.String(), "Error loading config") {
		t.Errorf("bad config: code %d err %q", code, errOut.String())
	}
	errOut.Reset()
	failing := func(*config.Config, string) error { return errors.New("no tty") }
	if code := run([]string{filepath.Join(t.TempDir(), "none.yaml")}, &out, &errOut, failing); code != 1 || !strings.Contains(errOut.String(), "no tty") {
		t.Errorf("tui error: code %d err %q", code, errOut.String())
	}
}
