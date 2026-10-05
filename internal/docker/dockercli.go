package docker

import (
	"strings"

	"github.com/alexbabintsev/laradok/internal/connection"
)

// DefaultDockerCLI is the docker invocation used when a server sets none.
const DefaultDockerCLI = "docker"

// dockerCLIer is implemented by runners that substitute the docker CLI.
type dockerCLIer interface {
	DockerCLI() string
}

// WithDockerCLI returns a runner that runs every `docker …` invocation of a
// host command as cli instead — e.g. "sudo -n docker" for users outside the
// docker group, or "podman". It works by defining a shell function named
// docker in front of each host command:
//
//	sh -c 'docker() { sudo -n docker "$@"; }; <command>'
//
// so every call is covered (including ones inside $(…)), while scripts that
// run inside containers, and any user data in quoted arguments, are left
// untouched. cli is the server's own configuration and is used verbatim.
func WithDockerCLI(r Runner, cli string) Runner {
	cli = strings.TrimSpace(cli)
	if cli == "" || cli == DefaultDockerCLI {
		return r
	}
	return dockerCLIRunner{inner: r, cli: cli}
}

// dockerPrelude defines the docker shell function for cli ("" when cli is the
// default).
func dockerPrelude(cli string) string {
	if cli == "" || cli == DefaultDockerCLI {
		return ""
	}
	// "command" bypasses the function itself when cli starts with "docker".
	if rest, ok := strings.CutPrefix(cli, "docker"); ok && (rest == "" || rest[0] == ' ') {
		cli = "command docker" + rest
	}
	return "docker() { " + cli + ` "$@"; }; `
}

// runnerDockerCLI returns the docker CLI a runner substitutes ("" = default).
func runnerDockerCLI(r Runner) string {
	if d, ok := r.(dockerCLIer); ok {
		return d.DockerCLI()
	}
	return ""
}

type dockerCLIRunner struct {
	inner Runner
	cli   string
}

func (d dockerCLIRunner) wrap(cmd string) string { return HostShCmd(dockerPrelude(d.cli) + cmd) }

func (d dockerCLIRunner) DockerCLI() string { return d.cli }

func (d dockerCLIRunner) RunCommand(cmd string) (string, error) {
	return d.inner.RunCommand(d.wrap(cmd))
}

func (d dockerCLIRunner) RunOutput(cmd, input string) (string, error) {
	return d.inner.RunOutput(d.wrap(cmd), input)
}

func (d dockerCLIRunner) StreamCommand(cmd, input string) (<-chan string, func(), error) {
	return d.inner.StreamCommand(d.wrap(cmd), input)
}

func (d dockerCLIRunner) InteractiveCommand(cmd string) (<-chan string, chan<- string, func(), error) {
	return d.inner.InteractiveCommand(d.wrap(cmd))
}

func (d dockerCLIRunner) StartCommand(cmd, input string) (*connection.Process, error) {
	return d.inner.StartCommand(d.wrap(cmd), input)
}

func (d dockerCLIRunner) Close() error { return d.inner.Close() }
