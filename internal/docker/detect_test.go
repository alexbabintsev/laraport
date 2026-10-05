package docker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeEnv(t *testing.T, dir string, env ...string) {
	t.Helper()
	data, _ := json.Marshal(env)
	writeFixture(t, dir, "env.json", string(data)+"\n")
}

func TestDetectPostgresCredentials(t *testing.T) {
	dir, r := fakeDocker(t)
	writeEnv(t, dir, "PATH=/bin", "POSTGRES_PASSWORD=a=b\nc")
	u, p, err := DetectPostgresCredentials(r, "c")
	if err != nil || u != "postgres" || p != "a=b\nc" {
		t.Fatalf("got %q %q %v", u, p, err)
	}
	writeEnv(t, dir, "POSTGRES_USER=app")
	if u, _, _ := DetectPostgresCredentials(r, "c"); u != "app" {
		t.Fatalf("user %q", u)
	}
}

func TestDetectMySQLCredentials(t *testing.T) {
	dir, r := fakeDocker(t)
	cases := []struct {
		env        []string
		user, pass string
	}{
		{[]string{"MYSQL_ROOT_PASSWORD=root", "MYSQL_USER=app", "MYSQL_PASSWORD=apppw"}, "root", "root"},
		{[]string{"MYSQL_USER=app", "MYSQL_PASSWORD=apppw"}, "app", "apppw"},
		{[]string{"MARIADB_ROOT_PASSWORD=mr"}, "root", "mr"},
		{[]string{"MARIADB_USER=mu", "MARIADB_PASSWORD=mp"}, "mu", "mp"},
		{nil, "root", ""},
	}
	for _, c := range cases {
		writeEnv(t, dir, c.env...)
		u, p, err := DetectMySQLCredentials(r, "c")
		if err != nil || u != c.user || p != c.pass {
			t.Errorf("%v → %q %q %v", c.env, u, p, err)
		}
	}
}

func TestDetectRedisPassword(t *testing.T) {
	dir, r := fakeDocker(t)
	cases := []struct {
		env  []string
		want string
	}{
		{[]string{"REDIS_PASSWORD=direct", "REDIS_URL=redis://:url@h"}, "direct"},
		{[]string{"REDIS_URL=redis://:p%23w@h:6379/0"}, "p#w"},
		{[]string{"REDIS_URL=redis://h:6379"}, ""},
		{nil, ""},
	}
	for _, c := range cases {
		writeEnv(t, dir, c.env...)
		if got, err := DetectRedisPassword(r, "c"); err != nil || got != c.want {
			t.Errorf("%v → %q %v", c.env, got, err)
		}
	}
}

func TestDetectMongoCredentials(t *testing.T) {
	dir, r := fakeDocker(t)
	writeEnv(t, dir, "MONGODB_URI=mongodb://uri:u%40p@h1,h2/db")
	u, p, err := DetectMongoCredentials(r, "c")
	if err != nil || u != "uri" || p != "u@p" {
		t.Fatalf("got %q %q %v", u, p, err)
	}
	writeEnv(t, dir, "MONGO_INITDB_ROOT_USERNAME=root", "MONGO_INITDB_ROOT_PASSWORD=rp", "MONGO_URL=mongodb://x:y@h")
	if u, p, _ := DetectMongoCredentials(r, "c"); u != "root" || p != "rp" {
		t.Fatalf("explicit vars should win: %q %q", u, p)
	}
}

func TestContainerEnvErrors(t *testing.T) {
	dir, r := fakeDocker(t)
	writeFixture(t, dir, "env.json", "not json")
	if _, err := containerEnv(r, "c"); err == nil || !strings.Contains(err.Error(), "parsing container env") {
		t.Fatalf("err = %v", err)
	}
	os.Remove(filepath.Join(dir, "env.json"))
	if _, err := containerEnv(r, "c"); err == nil {
		t.Fatal("want error when docker inspect fails")
	}
}

func TestListContainersFake(t *testing.T) {
	dir, r := fakeDocker(t)
	writeFixture(t, dir, "ps.out", `{"id":"a1","name":"web","image":"i","state":"running","status":"Up","ports":""}`+"\n")
	cs, err := ListContainers(r)
	if err != nil || len(cs) != 1 || cs[0].Name != "web" {
		t.Fatalf("got %+v %v", cs, err)
	}
	os.Remove(filepath.Join(dir, "ps.out"))
	if _, err := ListContainers(r); err == nil {
		t.Fatal("want error")
	}
}

func TestListContainerStatsFake(t *testing.T) {
	dir, r := fakeDocker(t)
	writeFixture(t, dir, "stats.out",
		`{"id":"0123456789ab","name":"web","cpu":"1.5%","mem":"10MiB / 1GiB","memperc":"1%","net":"1kB / 2kB","block":"0B / 0B","pids":"3"}`+"\nnoise\n")
	stats, err := ListContainerStats(r)
	if err != nil {
		t.Fatal(err)
	}
	if st, ok := LookupStat(stats, "web", ""); !ok || st.CPUPerc != "1.5%" || st.PIDs != "3" {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestSampleContainerStatsFake(t *testing.T) {
	dir, r := fakeDocker(t)
	writeFixture(t, dir, "stats.out", "noise\n"+`{"cpu":"50.00%","mem":"1GiB / 2GiB","net":"1MB / 1MB","block":"2kB / 0B"}`+"\n")
	s, err := SampleContainerStats(r, "c")
	if err != nil {
		t.Fatal(err)
	}
	if s.CPUPercent != 50 || s.MemBytes != 1<<30 || s.MemLimit != 2<<30 || s.NetTotal != 2<<20 || s.BlockTotal != 2048 {
		t.Fatalf("sample = %+v", s)
	}
	writeFixture(t, dir, "stats.out", "")
	if _, err := SampleContainerStats(r, "c"); err == nil {
		t.Fatal("want error for empty output")
	}
}

func TestInspectContainerFake(t *testing.T) {
	dir, r := fakeDocker(t)
	writeFixture(t, dir, "inspect.json", `[{"Id":"abc","Name":"/web","State":{"Status":"exited","Running":false,"ExitCode":137},
		"Config":{"Image":"php:8","Labels":{"b":"2","a":"1"}},
		"Mounts":[{"Type":"volume","Name":"data","Destination":"/data","RW":true}],
		"NetworkSettings":{"Networks":{"z":{"IPAddress":"10.0.0.2"},"a":{"IPAddress":"10.0.0.1"}}}}]`)
	info, err := InspectContainer(r, "abc")
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "web" || info.Image != "php:8" || info.Status != "Exited (exit 137)" {
		t.Fatalf("info = %+v", info)
	}
	if info.Networks[0].Name != "a" || info.Labels[0].Key != "a" || info.Mounts[0].Source != "data" {
		t.Fatalf("sorting/volume: %+v", info)
	}
	writeFixture(t, dir, "inspect.json", "[]")
	if _, err := InspectContainer(r, "abc"); err == nil {
		t.Fatal("want not found")
	}
}

func TestTopProcessesFake(t *testing.T) {
	_, r := fakeDocker(t)
	// The fake exec runs "ps" locally; replace it with a fixture printer.
	bin := filepath.Join(t.TempDir(), "bin")
	os.Mkdir(bin, 0o755)
	os.WriteFile(filepath.Join(bin, "ps"), []byte("#!/bin/sh\nprintf '  1  0.5  9.0 php-fpm\\n  2 80.0  1.0 php artisan\\n  3  2.0  3.0 nginx\\nbad\\n'\n"), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	byCPU, err := TopProcesses(r, "c", SortByCPU, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(byCPU) != 2 || byCPU[0].Command != "php artisan" || byCPU[1].Command != "nginx" {
		t.Fatalf("by cpu = %+v", byCPU)
	}
	byMem, _ := TopProcesses(r, "c", SortByMem, 0)
	if len(byMem) != 3 || byMem[0].PID != "1" {
		t.Fatalf("by mem = %+v", byMem)
	}
}

func TestDetectCapabilitiesFake(t *testing.T) {
	_, r := fakeDocker(t)
	root := filepath.Join(t.TempDir(), "my app")
	os.MkdirAll(root, 0o755)
	os.WriteFile(filepath.Join(root, "artisan"), nil, 0o644)
	caps, err := DetectCapabilities(r, "c", root+"/")
	if err != nil {
		t.Fatal(err)
	}
	if !caps.HasLaravel || caps.LaravelRoot != root {
		t.Fatalf("caps = %+v", caps)
	}
}

func TestListLogFilesFakeHostileNames(t *testing.T) {
	_, r := fakeDocker(t)
	root := t.TempDir()
	logs := filepath.Join(root, "storage", "logs")
	os.MkdirAll(logs, 0o755)
	names := []string{"a.log", "sp ace.log", "q'uote.log", `x$(touch PWNED).log`}
	for i, n := range names {
		os.WriteFile(filepath.Join(logs, n), []byte(strings.Repeat("line\n", i+1)), 0o644)
	}
	files, err := ListLogFiles(r, "c", root)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, f := range files {
		got[filepath.Base(f.Path)] = f.Lines
	}
	for i, n := range names {
		if got[n] != i+1 {
			t.Errorf("%q: lines %d (all: %v)", n, got[n], got)
		}
	}
	if _, err := os.Stat("PWNED"); err == nil {
		os.Remove("PWNED")
		t.Fatal("file name was executed")
	}
}

func TestLoadLogChunkAndCountFake(t *testing.T) {
	_, r := fakeDocker(t)
	p := filepath.Join(t.TempDir(), "big log.log")
	var b strings.Builder
	for i := 1; i <= 2500; i++ {
		b.WriteString("l")
		b.WriteString(strings.Repeat("x", i%3))
		b.WriteString("\n")
	}
	os.WriteFile(p, []byte(b.String()), 0o644)

	n, err := CountFileLines(r, "c", p)
	if err != nil || n != 2500 {
		t.Fatalf("count = %d %v", n, err)
	}
	lines, atTop, err := LoadLogChunk(r, "c", p, 1501)
	if err != nil || len(lines) != LogChunkSize || atTop {
		t.Fatalf("chunk: %d %v %v", len(lines), atTop, err)
	}
	lines, atTop, err = LoadLogChunk(r, "c", p, -5)
	if err != nil || len(lines) != LogChunkSize || !atTop {
		t.Fatalf("top chunk: %d %v %v", len(lines), atTop, err)
	}
	if _, _, err := LoadLogChunk(r, "c", p, 99999); err == nil {
		t.Fatal("want error past the end")
	}
	if _, err := CountFileLines(r, "c", p+".missing"); err == nil {
		t.Fatal("want error for a missing file")
	}
}

func TestListSQLiteDatabasesFake(t *testing.T) {
	_, r := fakeDocker(t)
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "database"), 0o755)
	os.WriteFile(filepath.Join(root, "database", "my db.sqlite"), nil, 0o644)
	dbs, err := ListSQLiteDatabases(r, "c", root)
	if err != nil {
		t.Fatal(err)
	}
	// root/database is searched directly and again under root (depth 3).
	if len(dbs) != 1 || filepath.Base(dbs[0]) != "my db.sqlite" {
		t.Fatalf("dbs = %q", dbs)
	}
}

func TestListDirFake(t *testing.T) {
	_, r := fakeDocker(t)
	d := t.TempDir()
	os.Mkdir(filepath.Join(d, "z dir"), 0o755)
	os.WriteFile(filepath.Join(d, "b'f"), []byte("12345"), 0o644)
	os.WriteFile(filepath.Join(d, ".h"), nil, 0o644)
	entries, err := ListDir(r, "c", d+"/")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name)
	}
	if strings.Join(names, ",") != "z dir,.h,b'f" {
		t.Fatalf("names = %q", names)
	}
	if entries[2].Path != d+"/b'f" {
		t.Fatalf("path = %q", entries[2].Path)
	}
}
