package docker

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/alexbabintsev/laraport/internal/connection"
)

// trickyPassword exercises quoting: quotes, $, backslash, spaces, @ and %.
const trickyPassword = `p'a"s$s w\o@r%d`

// readyExec waits until script succeeds inside the container.
func readyExec(t *testing.T, id, script string, timeout time.Duration) {
	t.Helper()
	waitFor(t, timeout, "container to become ready", func() bool {
		return exec.Command("docker", "exec", id, "sh", "-c", script).Run() == nil
	})
}

func streamAll(t *testing.T, r Runner, hc HostCommand) string {
	t.Helper()
	ch, stop, err := Stream(r, hc)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	return strings.Join(drain(t, ch, 60*time.Second), "\n")
}

func TestIntegrationPostgres(t *testing.T) {
	requireIntegration(t)
	r := connection.NewLocalClient()
	id := startContainer(t, "postgres:16-alpine",
		"-e", "POSTGRES_USER=app user", "-e", "POSTGRES_PASSWORD="+trickyPassword, "-e", "POSTGRES_DB=main")
	readyExec(t, id, `pg_isready -U "app user" -d main -h 127.0.0.1`, 60*time.Second)
	dexec(t, id, `psql -U "app user" -d main -c 'CREATE DATABASE "weird db|x"' -c 'CREATE TABLE items(id int, name text)' -c "INSERT INTO items VALUES (1, 'it''s')"`)

	user, pass, err := DetectPostgresCredentials(r, id)
	if err != nil || user != "app user" || pass != trickyPassword {
		t.Fatalf("creds = %q %q %v", user, pass, err)
	}
	dbs, err := ListDatabases(r, id, user, pass)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(dbs, ",") != "main,weird db|x" {
		t.Fatalf("dbs = %q", dbs)
	}
	// (Local socket connections use "trust" in the official image, so a wrong
	// password cannot be tested here.)

	out := streamAll(t, r, DBExecHostCmd(EnginePostgres, id, user, pass, "main", "SELECT name FROM items"))
	if !strings.Contains(out, "it's") {
		t.Fatalf("query output = %q", out)
	}

	t.Run("dumps", func(t *testing.T) {
		useTempDownloads(t)
		for _, f := range []PGDumpFormat{PGDumpPlain, PGDumpInserts, PGDumpCustom} {
			ch, stop, err := DumpPostgres(r, id, user, pass, "main", f)
			if err != nil {
				t.Fatal(err)
			}
			saved := savedPath(t, drain(t, ch, 60*time.Second))
			stop()
			switch f {
			case PGDumpCustom:
				data, _ := os.ReadFile(saved)
				if !strings.HasPrefix(string(data), "PGDMP") {
					t.Fatalf("custom dump header = %q", data[:min(5, len(data))])
				}
			case PGDumpInserts:
				if sql := gunzipString(t, saved); !strings.Contains(sql, "INSERT INTO public.items") {
					t.Fatalf("inserts dump lacks INSERT")
				}
			default:
				sql := gunzipString(t, saved)
				if !strings.Contains(sql, "CREATE TABLE public.items") {
					t.Fatalf("plain dump lacks CREATE TABLE")
				}
				for _, l := range strings.Split(sql, "\n") {
					if isRestrictLine([]byte(l)) {
						t.Fatalf("restrict line kept: %q", l)
					}
				}
			}
		}
	})

	t.Run("failed dump reports error", func(t *testing.T) {
		dir := useTempDownloads(t)
		ch, stop, err := DumpPostgres(r, id, user, pass, "no_such_db", PGDumpPlain)
		if err != nil {
			t.Fatal(err)
		}
		defer stop()
		lines := drain(t, ch, 60*time.Second)
		if last := lines[len(lines)-1]; !strings.HasPrefix(last, "ERROR") || !strings.Contains(last, "no_such_db") {
			t.Fatalf("want error naming the db, got %q", lines)
		}
		assertNoPartFiles(t, dir)
	})

	t.Run("password not in argv", func(t *testing.T) {
		hc := DBExecHostCmd(EnginePostgres, id, user, pass, "main", "SELECT pg_sleep(5)")
		ch, stop, err := Stream(r, hc)
		if err != nil {
			t.Fatal(err)
		}
		defer stop()
		waitFor(t, 10*time.Second, "query to run", func() bool { return processCount(t, id, "pg_sleep") >= 1 })
		if n := processCount(t, id, "w\\o@r"); n != 0 {
			t.Fatalf("password visible in %d process command lines", n)
		}
		stop()
		_ = ch
	})
}

func TestIntegrationMariaDB(t *testing.T) {
	requireIntegration(t)
	r := connection.NewLocalClient()
	id := startContainer(t, "mariadb:11", "-e", "MARIADB_ROOT_PASSWORD="+trickyPassword, "-e", "MARIADB_DATABASE=shop")
	readyExec(t, id, `mariadb -uroot -p"$MARIADB_ROOT_PASSWORD" -e 'SELECT 1'`, 90*time.Second)
	dexec(t, id, `mariadb -uroot -p"$MARIADB_ROOT_PASSWORD" shop -e "CREATE TABLE t(id int); INSERT INTO t VALUES (42);"`)

	caps, err := DetectCapabilities(r, id, "")
	if err != nil || !caps.HasMySQL || !caps.IsMariaDB || caps.DBEngine() != EngineMariaDB {
		t.Fatalf("caps = %+v %v", caps, err)
	}
	user, pass, err := DetectMySQLCredentials(r, id)
	if err != nil || user != "root" || pass != trickyPassword {
		t.Fatalf("creds = %q %q %v", user, pass, err)
	}
	dbs, err := ListMySQLDatabases(r, id, user, pass)
	if err != nil || strings.Join(dbs, ",") != "shop" {
		t.Fatalf("dbs = %q %v", dbs, err)
	}
	if out := streamAll(t, r, DBExecHostCmd(EngineMariaDB, id, user, pass, "shop", "SELECT id FROM t")); !strings.Contains(out, "42") {
		t.Fatalf("query output = %q", out)
	}

	useTempDownloads(t)
	ch, stop, err := DumpMySQLDatabase(r, id, user, pass, "shop")
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	saved := savedPath(t, drain(t, ch, 60*time.Second))
	if sql := gunzipString(t, saved); !strings.Contains(sql, "INSERT INTO `t` VALUES") {
		t.Fatalf("dump lacks data")
	}
}

func TestIntegrationRedis(t *testing.T) {
	requireIntegration(t)
	r := connection.NewLocalClient()
	id := startContainer(t, "redis:7-alpine", "-e", "REDIS_PASSWORD="+trickyPassword, "--",
		"sh", "-c", `exec redis-server --requirepass "$REDIS_PASSWORD"`)
	readyExec(t, id, `REDISCLI_AUTH="$REDIS_PASSWORD" redis-cli ping | grep -q PONG`, 30*time.Second)
	dexec(t, id, `REDISCLI_AUTH="$REDIS_PASSWORD" redis-cli set "k 1" v >/dev/null`)

	pass, err := DetectRedisPassword(r, id)
	if err != nil || pass != trickyPassword {
		t.Fatalf("password = %q %v", pass, err)
	}
	if out := streamAll(t, r, RedisCmd(id, pass, "PING")); out != "PONG" {
		t.Fatalf("PING = %q", out)
	}
	if out := streamAll(t, r, RedisCmd(id, pass, "--scan | head -20")); out != "k 1" {
		t.Fatalf("scan = %q", out)
	}

	useTempDownloads(t)
	ch, stop, err := DumpRedis(r, id, pass)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	saved := savedPath(t, drain(t, ch, 60*time.Second))
	data, _ := os.ReadFile(saved)
	if !strings.HasPrefix(string(data), "REDIS") {
		t.Fatalf("rdb header = %q", data[:min(5, len(data))])
	}
	if n := strings.TrimSpace(dexec(t, id, `ls /tmp | wc -l`)); n != "0" {
		t.Fatalf("temp files left in container: %s", n)
	}
}

func TestIntegrationMongo(t *testing.T) {
	requireIntegration(t)
	if exec.Command("docker", "image", "inspect", "mongo:7").Run() != nil {
		t.Skip("mongo:7 image not present")
	}
	r := connection.NewLocalClient()
	id := startContainer(t, "mongo:7",
		"-e", "MONGO_INITDB_ROOT_USERNAME=admin", "-e", "MONGO_INITDB_ROOT_PASSWORD="+trickyPassword)
	readyExec(t, id, `mongosh --quiet -u admin -p "$MONGO_INITDB_ROOT_PASSWORD" --eval 'db.runCommand({ping:1}).ok' | grep -q 1`, 90*time.Second)

	user, pass, err := DetectMongoCredentials(r, id)
	if err != nil || user != "admin" || pass != trickyPassword {
		t.Fatalf("creds = %q %q %v", user, pass, err)
	}
	out := streamAll(t, r, MongoEvalCmd(id, "mongosh", user, pass, "", "db.getSiblingDB('shop').items.insertOne({a:1}); print('count=' + db.getSiblingDB('shop').items.countDocuments())"))
	if !strings.Contains(out, "count=1") {
		t.Fatalf("eval output = %q", out)
	}

	useTempDownloads(t)
	ch, stop, err := DumpMongo(r, id, user, pass)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	saved := savedPath(t, drain(t, ch, 120*time.Second))
	if st, err := os.Stat(saved); err != nil || st.Size() < 100 {
		t.Fatalf("archive: %v %v", st, err)
	}
	if n := strings.TrimSpace(dexec(t, id, `ls /tmp | grep -c tmp. || true`)); n != "0" {
		t.Fatalf("config temp file left in container")
	}
}
