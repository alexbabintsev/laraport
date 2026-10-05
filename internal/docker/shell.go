package docker

import (
	"os/exec"
	"strconv"
)

// ShellTarget carries everything needed to open an interactive shell in a
// container, whether the host is local or reached over SSH.
type ShellTarget struct {
	ContainerID string

	// SSH connection details. When Host is empty the container is reached via
	// the local docker socket; otherwise the system `ssh` binary is used.
	Host       string
	Port       int
	User       string
	KeyPath    string
}

// innerShellCmd is the command run inside the container: clear the screen so no
// TUI remnants are left behind, then prefer bash, falling back to sh, so it
// works on both Debian- and Alpine-based images. The printf is a fallback for
// minimal images that ship no `clear` binary.
const innerShellCmd = `clear 2>/dev/null || printf '\033[2J\033[H'; exec $(command -v bash || command -v sh)`

// InteractiveShellCmd builds an *exec.Cmd that, when run with the real terminal
// attached (e.g. via tea.ExecProcess), opens an interactive shell inside the
// container. For local hosts it execs `docker exec -it`; for SSH hosts it shells
// out to `ssh -t` so the remote allocates a PTY.
func InteractiveShellCmd(t ShellTarget) *exec.Cmd {
	if t.Host == "" {
		return exec.Command("docker", "exec", "-it", t.ContainerID, "sh", "-c", innerShellCmd)
	}

	args := []string{"-t"}
	if t.Port != 0 && t.Port != 22 {
		args = append(args, "-p", strconv.Itoa(t.Port))
	}
	if t.KeyPath != "" {
		args = append(args, "-i", t.KeyPath)
	}
	dest := t.Host
	if t.User != "" {
		dest = t.User + "@" + t.Host
	}
	// "--" ends option parsing so a host value starting with "-" cannot be
	// read as an ssh option. The remote side joins the command words and
	// hands them to a shell, so each word is quoted individually.
	args = append(args, "--", dest,
		"docker", "exec", "-it", shellQuote(t.ContainerID), "sh", "-c", shellQuote(innerShellCmd),
	)
	return exec.Command("ssh", args...)
}
