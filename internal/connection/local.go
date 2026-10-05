package connection

import (
	"io"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// LocalClient runs commands on this machine with `sh -c` (for the local
// Docker socket).
type LocalClient struct {
	base
}

// NewLocalClient creates a LocalClient.
func NewLocalClient() *LocalClient {
	return &LocalClient{base{localTransport{}}}
}

// Close is a no-op; it exists so LocalClient and SSHClient share a lifecycle.
func (c *LocalClient) Close() error { return nil }

type localTransport struct{}

// localWaitDelay bounds how long Wait keeps copying output after the shell has
// exited, in case a stray background process still holds stdout open.
const localWaitDelay = 2 * time.Second

func (localTransport) start(cmd string, stdin io.Reader, stdout, stderr io.Writer) (*handle, error) {
	c := exec.Command("sh", "-c", cmd)
	c.Stdout = stdout
	c.Stderr = stderr
	c.WaitDelay = localWaitDelay
	// Own process group, so kill reaches the whole pipeline (docker CLI etc.),
	// not just the outer sh.
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Feed stdin through an explicit pipe pumped by our own goroutine: exec's
	// built-in copier would make Wait block until our reader hits EOF.
	var stdinPipe io.WriteCloser
	if stdin != nil {
		p, err := c.StdinPipe()
		if err != nil {
			return nil, err
		}
		stdinPipe = p
	}

	if err := c.Start(); err != nil {
		return nil, err
	}
	if stdinPipe != nil {
		go func() {
			io.Copy(stdinPipe, stdin) //nolint:errcheck // EPIPE once the process exits is expected
			stdinPipe.Close()
		}()
	}

	// exited guards against signalling a process group whose leader has been
	// reaped (its pid could be reused).
	var mu sync.Mutex
	exited := false
	return &handle{
		wait: func() error {
			err := c.Wait()
			mu.Lock()
			exited = true
			mu.Unlock()
			return err
		},
		kill: func() {
			mu.Lock()
			defer mu.Unlock()
			if !exited {
				// Negative pid targets the whole process group.
				syscall.Kill(-c.Process.Pid, syscall.SIGKILL) //nolint:errcheck
			}
		},
	}, nil
}
