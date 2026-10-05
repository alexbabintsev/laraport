package docker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexbabintsev/laradok/internal/connection"
)

// fakeDockerScript stands in for the docker CLI in hermetic tests:
//
//   - `docker exec [-i] [-e K=V]… ID cmd…` runs cmd locally with
//     FAKE_IN_CONTAINER=1 (stdin attached only with -i, like docker) and logs
//     the container ID it was given;
//   - `docker inspect --format '{{json .Config.Env}}' ID` prints $FAKE_DIR/env.json;
//   - `docker inspect ID`, `docker ps …`, `docker stats …` print
//     $FAKE_DIR/inspect.json, ps.out, stats.out;
//   - anything else fails.
const fakeDockerScript = `#!/bin/sh
# Skip global flags such as --log-level (value-less in tests).
while [ "${1#--}" != "$1" ]; do shift; done
cmd=$1; shift
case "$cmd" in
exec)
  interactive=0
  while [ $# -gt 0 ]; do
    case "$1" in
      -i) interactive=1; shift ;;
      -e) export "$2"; shift 2 ;;
      -*) shift ;;
      *) break ;;
    esac
  done
  printf '%s\n' "$1" >> "$FAKE_DIR/exec_ids"
  shift
  export FAKE_IN_CONTAINER=1
  if [ $interactive = 1 ]; then exec "$@"; else exec "$@" </dev/null; fi
  ;;
inspect)
  case "$1" in
    --format) cat "$FAKE_DIR/env.json" ;;
    *) cat "$FAKE_DIR/inspect.json" ;;
  esac
  ;;
ps) cat "$FAKE_DIR/ps.out" ;;
stats) cat "$FAKE_DIR/stats.out" ;;
*) echo "fake docker: unsupported command $cmd" >&2; exit 64 ;;
esac
`

// fakeDocker installs the fake docker CLI first in PATH and returns its data
// directory (for fixtures) and a LocalClient that will use it.
func fakeDocker(t *testing.T) (string, Runner) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(fakeDockerScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_DIR", dir)
	return dir, connection.NewLocalClient()
}

// writeFixture writes a fixture file into the fake docker data dir.
func writeFixture(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// execIDs returns the container IDs the fake docker exec received.
func execIDs(t *testing.T, dir string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "exec_ids"))
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}
