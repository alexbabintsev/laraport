package connection

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"sync"
)

// LocalClient runs Docker commands via the local Docker socket.
type LocalClient struct{}

// NewLocalClient creates a LocalClient.
func NewLocalClient() *LocalClient {
	return &LocalClient{}
}

// RunCommand runs a shell command locally and returns combined output.
func (c *LocalClient) RunCommand(cmd string) (string, error) {
	out, err := exec.Command("sh", "-c", cmd).CombinedOutput()
	return string(out), err
}

// StreamCommand runs a command locally and streams output line-by-line.
func (c *LocalClient) StreamCommand(cmd string) (<-chan string, func(), error) {
	command := exec.Command("sh", "-c", cmd)

	pr, pw := io.Pipe()
	command.Stdout = pw
	command.Stderr = pw

	if err := command.Start(); err != nil {
		pw.Close()
		pr.Close()
		return nil, nil, fmt.Errorf("start command: %w", err)
	}

	ch := make(chan string, 64)
	quit := make(chan struct{})
	done := make(chan struct{})

	var quitOnce sync.Once

	stop := func() {
		quitOnce.Do(func() { close(quit) })
		if command.Process != nil {
			command.Process.Kill() //nolint:errcheck
		}
		pw.Close()
		<-done
	}

	go func() {
		defer close(done)
		defer close(ch)

		go func() {
			command.Wait() //nolint:errcheck
			pw.Close()
		}()

		scanner := bufio.NewScanner(pr)
		for scanner.Scan() {
			select {
			case ch <- scanner.Text():
			case <-quit:
				return
			}
		}
	}()

	return ch, stop, nil
}

// InteractiveCommand runs a command locally, streaming output and accepting stdin input.
// Returns: output channel, stdin channel (send lines to write to process stdin), stop func, error.
func (c *LocalClient) InteractiveCommand(cmd string) (<-chan string, chan<- string, func(), error) {
	command := exec.Command("sh", "-c", cmd)

	pr, pw := io.Pipe()
	command.Stdout = pw
	command.Stderr = pw

	stdinPipe, err := command.StdinPipe()
	if err != nil {
		pw.Close()
		pr.Close()
		return nil, nil, nil, fmt.Errorf("stdin pipe: %w", err)
	}

	if err := command.Start(); err != nil {
		pw.Close()
		pr.Close()
		return nil, nil, nil, fmt.Errorf("start command: %w", err)
	}

	outCh := make(chan string, 64)
	inCh := make(chan string, 16)

	stop := func() {
		stdinPipe.Close()
		if command.Process != nil {
			command.Process.Kill() //nolint:errcheck
		}
		pw.Close()
	}

	// Forward stdin lines to process
	go func() {
		defer stdinPipe.Close()
		for line := range inCh {
			fmt.Fprintln(stdinPipe, line) //nolint:errcheck
		}
	}()

	go func() {
		defer close(outCh)

		go func() {
			command.Wait() //nolint:errcheck
			pw.Close()
		}()

		scanner := bufio.NewScanner(pr)
		for scanner.Scan() {
			outCh <- scanner.Text()
		}
	}()

	return outCh, inCh, stop, nil
}

