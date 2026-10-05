package docker

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestParseContainers(t *testing.T) {
	out := strings.Join([]string{
		"bash: warning: setlocale: LC_ALL: cannot change locale", // rc noise on stdout
		`{"id":"abc123","name":"/web","image":"php:8","state":"running","status":"Up 3 hours (healthy)","ports":"0.0.0.0:80->80/tcp"}`,
		`{"id":"def456","name":"we\"ird","image":"x","state":"exited","status":"Exited (0)","ports":""}`,
		`{"id":"","name":"broken"}`,
		`{"id":"trunc`,
		"",
	}, "\n")
	got := parseContainers(out)
	want := []Container{
		{ID: "abc123", Name: "web", Image: "php:8", State: "running", Status: "Up 3 hours (healthy)", Ports: "0.0.0.0:80->80/tcp"},
		{ID: "def456", Name: `we"ird`, Image: "x", State: "exited", Status: "Exited (0)"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
}

func TestParseArtisanCommands(t *testing.T) {
	out := "about                Display basic information\n" +
		"cache:clear          Flush the application cache\n" +
		"inspire\n" +
		"\n"
	got := parseArtisanCommands(out)
	want := []ArtisanCommand{
		{"about", "Display basic information"},
		{"cache:clear", "Flush the application cache"},
		{"inspire", ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
}

func TestParseComposerCommands(t *testing.T) {
	out := `Composer version 2.7.1

Usage:
  command [options] [arguments]

Options:
  -h, --help                     Display help

Available commands:
  about                Shows a short information about Composer
  dump-autoload        [dumpautoload] Dumps the autoloader
  install              [i] Installs the project dependencies
 config
  config:list          Lists config`
	got := parseComposerCommands(out)
	var names []string
	for _, c := range got {
		names = append(names, c.Name)
	}
	if strings.Join(names, ",") != "about,dump-autoload,install,config,config:list" {
		t.Fatalf("names = %q", names)
	}
	if got[0].Desc != "Shows a short information about Composer" {
		t.Fatalf("desc = %q", got[0].Desc)
	}
}

func TestParseNpmCommands(t *testing.T) {
	out := `Lifecycle scripts included in app@1.0.0:
  test
    jest
Scripts available in app@1.0.0 via ` + "`npm run-script`" + `:
  dev
    vite
  build
    vite build
  lint`
	got := parseNpmCommands(out)
	want := []ArtisanCommand{{"test", "jest"}, {"dev", "vite"}, {"build", "vite build"}, {"lint", ""}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
}

func TestParseFileStats(t *testing.T) {
	out := "1024|1700000000|0|1690000000|/var/log/a.log\n" +
		"10|1700000001|1600000000|1690000000|/var/log/pipe|name.log\n" +
		"garbage line\n" +
		"0|0|0|0|\n" +
		" 5|x|y|z|/weird\r\n"
	got := parseFileStats(out)
	if len(got) != 3 {
		t.Fatalf("got %d entries: %+v", len(got), got)
	}
	a := got[0]
	if a.Path != "/var/log/a.log" || a.Size != 1024 || a.Lines != -1 ||
		!a.ModifiedAt.Equal(time.Unix(1700000000, 0)) || !a.CreatedAt.Equal(time.Unix(1690000000, 0)) {
		t.Fatalf("a = %+v", a)
	}
	if got[1].Path != "/var/log/pipe|name.log" || !got[1].CreatedAt.Equal(time.Unix(1600000000, 0)) {
		t.Fatalf("pipe entry = %+v", got[1])
	}
	if got[2].Path != "/weird" || got[2].Size != 5 {
		t.Fatalf("weird entry = %+v", got[2])
	}
}

func TestParseLineCount(t *testing.T) {
	cases := []struct {
		in   string
		path string
		n    int
		ok   bool
	}{
		{"42|/var/log/a.log", "/var/log/a.log", 42, true},
		{" 7|/p|ipe", "/p|ipe", 7, true},
		{"x|/a", "", 0, false},
		{"5|", "", 0, false},
		{"noise", "", 0, false},
	}
	for _, c := range cases {
		p, n, ok := ParseLineCount(c.in)
		if p != c.path || n != c.n || ok != c.ok {
			t.Errorf("%q → %q %d %v", c.in, p, n, ok)
		}
	}
}

func TestParseDatabaseList(t *testing.T) {
	got := parseDatabaseList("main\nweird db|x\n\npostgres\nmain\r\n  \n", map[string]bool{"postgres": true})
	if !reflect.DeepEqual(got, []string{"main", "weird db|x"}) {
		t.Fatalf("got %q", got)
	}
}

func TestParseURLCreds(t *testing.T) {
	cases := []struct {
		in         string
		user, pass string
		ok         bool
	}{
		{"redis://:secret@redis:6379/0", "", "secret", true},
		{"redis://user:p%40ss%2Fw@host", "user", "p@ss/w", true},
		{"mongodb://admin:pw@h1:27017,h2:27017/db?replicaSet=rs", "admin", "pw", true},
		{"mongodb+srv://u:p@cluster.example.com/", "u", "p", true},
		{"redis://host:6379", "", "", false},
		{"not a url", "", "", false},
		{"", "", "", false},
		{"redis://u:p@ss@host", "u", "p@ss", true},
		{"redis://u@host", "u", "", true},
	}
	for _, c := range cases {
		u, p, ok := parseURLCreds(c.in)
		if u != c.user || p != c.pass || ok != c.ok {
			t.Errorf("%q → %q %q %v", c.in, u, p, ok)
		}
	}
}

func TestContainerLogService(t *testing.T) {
	cases := map[string]string{
		"/var/log/nginx/error.log": "nginx",
		"/var/log/php8.2-fpm.log":  "php8.2-fpm",
		"/var/log/syslog":          "syslog",
		"/var/log/a/b/c/deep.log":  "a",
	}
	for in, want := range cases {
		if got := containerLogService(in); got != want {
			t.Errorf("%s → %s, want %s", in, got, want)
		}
	}
}

func TestYAMLQuote(t *testing.T) {
	for _, s := range hostile {
		var v struct {
			Password string `yaml:"password"`
		}
		doc := "password: " + yamlQuote(s)
		if strings.Contains(doc[len("password: "):], "\n") {
			t.Fatalf("%q quoted onto several lines", s)
		}
		if err := yaml.Unmarshal([]byte(doc), &v); err != nil {
			t.Fatalf("%q: %v", s, err)
		}
		if v.Password != s {
			t.Errorf("%q → %q", s, v.Password)
		}
	}
}

func TestSafeFileName(t *testing.T) {
	cases := map[string]string{
		"app/1":        "app_1",
		"my db":        "my_db",
		"../../etc":    "_.._etc",
		"..":           "download",
		"":             "download",
		"a\x00b\nc":    "a_b_c",
		"c:\\x":        "c__x",
		"ok-name_1.db": "ok-name_1.db",
	}
	for in, want := range cases {
		if got := safeFileName(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}

func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{
		0:             "0 B",
		1023:          "1023 B",
		2048:          "2 KB",
		5 << 20:       "5.0 MB",
		3 << 30:       "3.00 GB",
		1<<20 + 1<<19: "1.5 MB",
	}
	for in, want := range cases {
		if got := humanBytes(in); got != want {
			t.Errorf("%d → %q, want %q", in, got, want)
		}
	}
}

func TestRootPaths(t *testing.T) {
	cases := []struct{ root, artisan, logs, storage, app string }{
		{"", "/var/www/html/artisan", "/var/www/html/storage/logs", "/var/www/html/storage", "/var/www/html"},
		{"/app/", "/app/artisan", "/app/storage/logs", "/app/storage", "/app"},
		{"/", "/artisan", "/storage/logs", "/storage", "/"},
		{"/srv/my app", "/srv/my app/artisan", "/srv/my app/storage/logs", "/srv/my app/storage", "/srv/my app"},
	}
	for _, c := range cases {
		if got := artisanPath(c.root); got != c.artisan {
			t.Errorf("artisanPath(%q) = %q", c.root, got)
		}
		if got := logsDir(c.root); got != c.logs {
			t.Errorf("logsDir(%q) = %q", c.root, got)
		}
		if got := storageDir(c.root); got != c.storage {
			t.Errorf("storageDir(%q) = %q", c.root, got)
		}
		if got := appRoot(c.root); got != c.app {
			t.Errorf("appRoot(%q) = %q", c.root, got)
		}
	}
}

func TestSQLiteSearchDirs(t *testing.T) {
	got := sqliteSearchDirs("/app/")
	want := []string{"/app/database", "/app/storage", "/app", "/data"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q", got)
	}
}

func TestArchiveScript(t *testing.T) {
	cases := map[string]string{
		"/var/www/html/storage/": "tar -czf - -C '/var/www/html' 'storage'",
		"/data/my dir":           "tar -czf - -C '/data' 'my dir'",
		"/":                      "tar -czf - -C '/' '/'",
		"rel":                    "tar -czf - -C '.' 'rel'",
	}
	for in, prefix := range cases {
		if got := archiveScript(in); !strings.HasPrefix(got, prefix+";") {
			t.Errorf("archiveScript(%q) = %q", in, got)
		}
	}
}

func TestDBEngine(t *testing.T) {
	for _, e := range []DBEngine{EngineMySQL, EngineMariaDB, EnginePercona} {
		if !e.IsMySQLFamily() || !e.UsesCredentials() {
			t.Errorf("%s family/creds", e)
		}
	}
	if EnginePostgres.IsMySQLFamily() || EngineSQLite.UsesCredentials() {
		t.Error("postgres/sqlite flags")
	}
	labels := map[DBEngine]string{EnginePostgres: "PostgreSQL", EngineMySQL: "MySQL", EngineMariaDB: "MariaDB", EnginePercona: "Percona", EngineSQLite: "SQLite"}
	for e, l := range labels {
		if e.Label() != l {
			t.Errorf("%s label %q", e, e.Label())
		}
	}
}

func TestContainerCapsEngine(t *testing.T) {
	cases := []struct {
		caps ContainerCaps
		want DBEngine
	}{
		{ContainerCaps{HasPostgres: true, HasMySQL: true}, EnginePostgres},
		{ContainerCaps{HasMySQL: true, IsMariaDB: true}, EngineMariaDB},
		{ContainerCaps{HasMySQL: true, IsPercona: true}, EnginePercona},
		{ContainerCaps{HasMySQL: true}, EngineMySQL},
		{ContainerCaps{HasSQLite: true}, EngineSQLite},
	}
	for _, c := range cases {
		if got := c.caps.DBEngine(); got != c.want {
			t.Errorf("%+v → %s", c.caps, got)
		}
		if !c.caps.HasDatabase() {
			t.Errorf("%+v HasDatabase false", c.caps)
		}
	}
	if (ContainerCaps{}).HasDatabase() || (ContainerCaps{}).HasMongo() || !(ContainerCaps{MongoBin: "mongo"}).HasMongo() {
		t.Error("empty caps")
	}
}

func TestDBExecHostCmdDispatch(t *testing.T) {
	pg := DBExecHostCmd(EnginePostgres, "c", "u", "pw", "db", "SELECT 1")
	my := DBExecHostCmd(EngineMariaDB, "c", "u", "pw", "db", "SELECT 1")
	sq := DBExecHostCmd(EngineSQLite, "c", "", "", "/data/x.db", "SELECT 1")
	if !strings.Contains(pg.Cmd, "psql") || pg.Input != "pw\n" {
		t.Errorf("pg = %+v", pg)
	}
	if !strings.Contains(my.Cmd, "mariadb") || my.Input != "pw\n" {
		t.Errorf("mysql = %+v", my)
	}
	if !strings.Contains(sq.Cmd, "sqlite3") || sq.Input != "" {
		t.Errorf("sqlite = %+v", sq)
	}
	for _, hc := range []HostCommand{pg, my} {
		if strings.Contains(hc.Cmd, "pw") {
			t.Errorf("password in command line: %s", hc.Cmd)
		}
	}
}

func TestMongoEvalCmd(t *testing.T) {
	sh := MongoEvalCmd("c", "mongosh", "admin", "s3cret", "shop", "db.stats()")
	if strings.Contains(sh.Cmd, "s3cret") || !strings.Contains(sh.Cmd, "process.env.LARADOK_MONGO_PASS") {
		t.Errorf("mongosh = %s", sh.Cmd)
	}
	if sh.Input != "admin\ns3cret\n" {
		t.Errorf("input = %q", sh.Input)
	}
	legacy := MongoEvalCmd("c", "mongo", "admin", "s3cret", "", "db.stats()")
	if strings.Contains(legacy.Cmd, "s3cret") || !strings.Contains(legacy.Cmd, `-p "$LARADOK_MONGO_PASS"`) {
		t.Errorf("legacy = %s", legacy.Cmd)
	}
	anon := MongoEvalCmd("c", "mongosh", "", "", "", "db.stats()")
	if anon.Input != "" || strings.Contains(anon.Cmd, "auth(") {
		t.Errorf("anon = %+v", anon)
	}
}

func TestRedisCmd(t *testing.T) {
	hc := RedisCmd("c", "pw", "  INFO server ")
	if strings.Contains(hc.Cmd, "pw") || hc.Input != "pw\n" || !strings.Contains(hc.Cmd, "redis-cli INFO server") {
		t.Errorf("hc = %+v", hc)
	}
	if hc := RedisCmd("c", "", "PING"); hc.Input != "" || strings.Contains(hc.Cmd, "REDISCLI_AUTH") {
		t.Errorf("no-auth hc = %+v", hc)
	}
}

func TestStatsParsing(t *testing.T) {
	if v := parsePercent(" 12.5% "); v != 12.5 {
		t.Errorf("percent %v", v)
	}
	if v := parsePercent("--"); v != 0 {
		t.Errorf("percent -- %v", v)
	}
	u, l := parseUsageLimit("120MiB / 1GiB")
	if u != 120*1024*1024 || l != 1024*1024*1024 {
		t.Errorf("usage %v limit %v", u, l)
	}
	if s := parsePairSum("1.5kB / 500B"); s != 1.5*1024+500 {
		t.Errorf("pair sum %v", s)
	}
	for in, want := range map[string]float64{"0B": 0, "": 0, "2GB": 2 << 30, "3k": 3072} {
		if got := parseSize(in); got != want {
			t.Errorf("parseSize(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestLookupStat(t *testing.T) {
	stats := map[string]ContainerStat{
		"web":          {Name: "web"},
		"0123456789ab": {Name: "by-id"},
	}
	if st, ok := LookupStat(stats, "web", "zzz"); !ok || st.Name != "web" {
		t.Error("by name")
	}
	if st, ok := LookupStat(stats, "other", "0123456789abcdef"); !ok || st.Name != "by-id" {
		t.Error("by short id")
	}
	if _, ok := LookupStat(stats, "x", "y"); ok {
		t.Error("missing")
	}
}

func TestInteractiveShellCmd(t *testing.T) {
	local := InteractiveShellCmd(ShellTarget{ContainerID: "abc"})
	if local.Args[0] != "docker" || strings.Join(local.Args[1:5], " ") != "exec -it abc sh" {
		t.Errorf("local args %q", local.Args)
	}
	remote := InteractiveShellCmd(ShellTarget{ContainerID: "abc", Host: "-oProxyCommand=x", Port: 2222, User: "deploy", KeyPath: "/k"})
	args := strings.Join(remote.Args, " ")
	if !strings.HasPrefix(args, "ssh -t -p 2222 -i /k -- deploy@-oProxyCommand=x sh -c 'docker exec -it '\\''abc'\\'' sh -c ") {
		t.Errorf("remote args %q", args)
	}
	sudo := InteractiveShellCmd(ShellTarget{ContainerID: "abc", Host: "h", DockerCLI: "sudo docker"})
	if a := strings.Join(sudo.Args, " "); !strings.Contains(a, "docker() { sudo docker") {
		t.Errorf("custom CLI not applied over ssh: %q", a)
	}
	localSudo := InteractiveShellCmd(ShellTarget{ContainerID: "abc", DockerCLI: "podman"})
	if localSudo.Args[0] != "sh" || !strings.Contains(localSudo.Args[2], "docker() { podman \"$@\"; }; docker exec -it 'abc'") {
		t.Errorf("custom CLI not applied locally: %q", localSudo.Args)
	}
	viaJ := InteractiveShellCmd(ShellTarget{ContainerID: "a", Host: "h", User: "u", JumpUser: "ops", JumpHost: "bastion", JumpPort: 2222})
	if a := strings.Join(viaJ.Args, " "); !strings.Contains(a, "-J ops@bastion:2222 -- u@h") {
		t.Errorf("-J args %q", a)
	}
	viaV6 := InteractiveShellCmd(ShellTarget{ContainerID: "a", Host: "h", JumpHost: "2001:db8::1"})
	if a := strings.Join(viaV6.Args, " "); !strings.Contains(a, "-J [2001:db8::1] -- h") {
		t.Errorf("IPv6 -J args %q", a)
	}
	viaKey := InteractiveShellCmd(ShellTarget{ContainerID: "a", Host: "h", JumpUser: "ops", JumpHost: "bastion", JumpPort: 2222, JumpKey: "/keys/my key"})
	if a := strings.Join(viaKey.Args, " "); !strings.Contains(a, "-o ProxyCommand=ssh -i '/keys/my key' -W %h:%p -p 2222 -l 'ops' -- 'bastion' -- h") ||
		strings.Contains(a, "-J") {
		t.Errorf("ProxyCommand args %q", a)
	}
	if def := InteractiveShellCmd(ShellTarget{ContainerID: "a", Host: "h", Port: 22}); strings.Contains(strings.Join(def.Args, " "), "-p") {
		t.Errorf("default port passed: %q", def.Args)
	}
}
