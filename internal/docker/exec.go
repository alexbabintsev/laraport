package docker

import (
	"fmt"
	"strings"
)

// ShellQuote wraps s in single quotes, escaping internal single quotes.
// The result is a single word for a POSIX shell with nothing expanded inside.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// shellQuote is an internal alias.
func shellQuote(s string) string { return ShellQuote(s) }

// HostCommand is a command line for the host shell plus the data to write to
// its stdin. Secrets travel in Input, never in Cmd, so they do not show up in
// process listings on the host or in the container.
type HostCommand struct {
	Cmd   string
	Input string
}

// Secret is an environment variable exported inside the container whose value
// is delivered through stdin.
type Secret struct {
	Name  string // a valid shell identifier, e.g. "PGPASSWORD"
	Value string // must not contain a newline
}

// ExecShCmd builds the host command that runs script with `sh -c` inside the
// container. The container ID and the script are both quoted as single words,
// so the host shell passes them through verbatim: variables, $(…), pipes and
// redirections in script are interpreted by the shell inside the container,
// never on the host. Any untrusted value embedded in script (file paths, DB
// names, user names) must itself be wrapped in shellQuote.
func ExecShCmd(containerID, script string) string {
	return "docker exec " + shellQuote(containerID) + " sh -c " + shellQuote(script)
}

// ExecShInteractiveCmd is ExecShCmd with `docker exec -i`, keeping the
// container process's stdin attached to ours.
func ExecShInteractiveCmd(containerID, script string) string {
	return "docker exec -i " + shellQuote(containerID) + " sh -c " + shellQuote(script)
}

// HostShCmd wraps script in `sh -c` on the host itself.
func HostShCmd(script string) string {
	return "sh -c " + shellQuote(script)
}

// ExecScript builds a one-shot in-container command. Secrets are read from
// stdin and exported before script runs.
func ExecScript(containerID, script string, secrets ...Secret) HostCommand {
	if len(secrets) == 0 {
		return HostCommand{Cmd: ExecShCmd(containerID, script)}
	}
	return HostCommand{
		Cmd:   ExecShInteractiveCmd(containerID, readSecrets(secrets)+script),
		Input: secretInput(secrets),
	}
}

// ExecStreamScript builds an in-container command for a stream that may be
// cancelled: script runs in the background and is terminated as soon as our
// end of stdin closes (the stream is stopped). Without this, closing a remote
// `docker exec` leaves the process running inside the container. Secrets are
// read from stdin first, as with ExecScript.
func ExecStreamScript(containerID, script string, secrets ...Secret) HostCommand {
	return HostCommand{
		Cmd:   ExecShInteractiveCmd(containerID, readSecrets(secrets)+stoppable(script)),
		Input: secretInput(secrets),
	}
}

// HostStreamScript is ExecStreamScript for a script that runs on the host
// itself (e.g. `docker logs -f`).
func HostStreamScript(script string) HostCommand {
	return HostCommand{Cmd: HostShCmd(stoppable(script))}
}

// stoppable wraps script so that it runs as a background job (with its own
// process group where the shell supports `set -m`) and is sent SIGTERM when
// stdin reaches EOF. The wrapper exits with the job's status as soon as the job
// ends on its own.
//
// fd 3 keeps the original stdin for the watcher: POSIX shells otherwise give
// background jobs /dev/null as stdin. wait's stderr is discarded because
// bash (when it is /bin/sh) prints job-control notices like "[1]+ Done …"
// there; the job's own stderr is unaffected.
func stoppable(script string) string {
	return `set -m 2>/dev/null; exec 3<&0; ` +
		`( ` + script + "\n" + ` ) </dev/null 3<&- & pid=$!; ` +
		`{ read _ <&3; kill -TERM -$pid 2>/dev/null || kill -TERM $pid 2>/dev/null; } >/dev/null 2>&1 & ` +
		`exec 3<&-; wait $pid 2>/dev/null`
}

// readSecrets returns a script prefix that reads one stdin line per secret and
// exports it.
func readSecrets(secrets []Secret) string {
	var b strings.Builder
	for _, s := range secrets {
		fmt.Fprintf(&b, "IFS= read -r %s; export %s; ", s.Name, s.Name)
	}
	return b.String()
}

// secretInput is the stdin payload matching readSecrets. A newline inside a
// value cannot be represented in this line-based protocol and is dropped.
func secretInput(secrets []Secret) string {
	var b strings.Builder
	for _, s := range secrets {
		b.WriteString(strings.NewReplacer("\n", "", "\r", "").Replace(s.Value))
		b.WriteByte('\n')
	}
	return b.String()
}

// gzipPipe runs `producer | gzip` but exits with the producer's status (a
// plain POSIX pipeline reports gzip's, hiding e.g. a failed pg_dump). The
// producer's status is passed out through fd 3 of a command substitution
// while gzip writes to the real stdout via fd 4.
func gzipPipe(producer string) string {
	return `exec 4>&1; s=$( { { ( ` + producer + "\n" + ` ); echo $? >&3; } | gzip >&4; } 3>&1 ); exit ${s:-1}`
}
