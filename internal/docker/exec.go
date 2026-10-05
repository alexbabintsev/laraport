package docker

import "strings"

// ShellQuote wraps s in single quotes, escaping internal single quotes.
// The result is a single word for a POSIX shell with nothing expanded inside.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// shellQuote is an internal alias.
func shellQuote(s string) string { return ShellQuote(s) }

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

// HostShCmd wraps script in `sh -c` on the host itself. Used for the
// `read X; kill $PID` stop trick around host-level commands such as
// `docker logs -f`.
func HostShCmd(script string) string {
	return "sh -c " + shellQuote(script)
}

// stripNUL removes NUL bytes that can show up in SSH output right after a
// connection opens.
func stripNUL(s string) string {
	return strings.ReplaceAll(s, "\x00", "")
}
