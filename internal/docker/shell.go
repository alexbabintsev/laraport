package docker

import (
	"os/exec"
	"strconv"
	"strings"
)

// ShellTarget carries everything needed to open an interactive shell in a
// container, whether the host is local or reached over SSH.
type ShellTarget struct {
	ContainerID string

	// DockerCLI replaces the docker command (e.g. "sudo docker"); "" = docker.
	DockerCLI string

	// SSH connection details. When Host is empty the container is reached via
	// the local docker socket; otherwise the system `ssh` binary is used.
	Host    string
	Port    int
	User    string
	KeyPath string

	// Jump host (bastion); JumpHost "" = direct. JumpKey "" = ssh's own
	// defaults and agent.
	JumpUser string
	JumpHost string
	JumpPort int
	JumpKey  string
}

// jumpSpec renders the jump host for `ssh -J`: [user@]host[:port], with
// IPv6 addresses in brackets.
func (t ShellTarget) jumpSpec() string {
	host := t.JumpHost
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if t.JumpUser != "" {
		host = t.JumpUser + "@" + host
	}
	if t.JumpPort != 0 && t.JumpPort != 22 {
		host += ":" + strconv.Itoa(t.JumpPort)
	}
	return host
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
	// The docker invocation as host shell text (with the docker function of a
	// custom docker CLI in front).
	dockerExec := dockerPrelude(t.DockerCLI) + "docker exec -it " + shellQuote(t.ContainerID) + " sh -c " + shellQuote(innerShellCmd)

	if t.Host == "" {
		if t.DockerCLI == "" || t.DockerCLI == DefaultDockerCLI {
			return exec.Command("docker", "exec", "-it", t.ContainerID, "sh", "-c", innerShellCmd)
		}
		return exec.Command("sh", "-c", dockerExec)
	}

	args := []string{"-t"}
	if t.Port != 0 && t.Port != 22 {
		args = append(args, "-p", strconv.Itoa(t.Port))
	}
	if t.KeyPath != "" {
		args = append(args, "-i", t.KeyPath)
	}
	switch {
	case t.JumpHost != "" && t.JumpKey == "":
		args = append(args, "-J", t.jumpSpec())
	case t.JumpHost != "":
		// ssh -J cannot take a key for the jump hop: tunnel with an explicit
		// ProxyCommand instead (run by ssh through the user's shell, hence
		// the quoting; %h/%p are filled in by ssh).
		proxy := "ssh -i " + shellQuote(t.JumpKey) + " -W %h:%p"
		if t.JumpPort != 0 && t.JumpPort != 22 {
			proxy += " -p " + strconv.Itoa(t.JumpPort)
		}
		if t.JumpUser != "" {
			proxy += " -l " + shellQuote(t.JumpUser)
		}
		proxy += " -- " + shellQuote(t.JumpHost)
		args = append(args, "-o", "ProxyCommand="+proxy)
	}
	dest := t.Host
	if t.User != "" {
		dest = t.User + "@" + t.Host
	}
	// "--" ends option parsing so a host value starting with "-" cannot be
	// read as an ssh option. The remote side hands the command to the login
	// shell, so it is wrapped in sh -c as one quoted word.
	args = append(args, "--", dest, HostShCmd(dockerExec))
	return exec.Command("ssh", args...)
}
