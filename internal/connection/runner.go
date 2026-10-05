// Package connection runs shell commands on the target host — locally or over
// SSH — and exposes them as one-shot calls, line streams, interactive
// sessions and raw byte streams.
package connection

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
)

// transport starts a single host command. stdin may be nil (no input). The
// returned handle must be waited on exactly once.
type transport interface {
	start(cmd string, stdin io.Reader, stdout, stderr io.Writer) (*handle, error)
}

// handle controls a started command.
type handle struct {
	// wait blocks until the command exits and its output has been copied.
	wait func() error
	// kill force-terminates the command's host-side process/session.
	kill func()
}

// maxLineLen caps a single streamed line. Longer lines are split into chunks
// of this size instead of aborting the stream.
const maxLineLen = 1 << 20

// stderrTailSize is how much trailing stderr a Process keeps for error messages.
const stderrTailSize = 4 << 10

// base implements the public Runner API on top of a transport.
type base struct {
	t transport
}

// RunCommand runs a command and returns its combined stdout+stderr output.
func (b base) RunCommand(cmd string) (string, error) {
	return b.RunCommandInput(cmd, "")
}

// RunCommandInput is RunCommand with input written to the command's stdin
// (followed by EOF). Use it to hand secrets to a command without putting them
// on its command line.
func (b base) RunCommandInput(cmd, input string) (string, error) {
	var out lockedBuffer
	var stdin io.Reader
	if input != "" {
		stdin = strings.NewReader(input)
	}
	h, err := b.t.start(cmd, stdin, &out, &out)
	if err != nil {
		return "", err
	}
	err = h.wait()
	return out.String(), err
}

// RunOutput runs a command (with optional stdin input) and returns only its
// stdout — for output that gets parsed, so noise on stderr (shell rc files,
// CLI warnings) cannot leak into the data. On failure the error carries the
// tail of stderr.
func (b base) RunOutput(cmd, input string) (string, error) {
	var out lockedBuffer
	stderr := &tailBuffer{max: stderrTailSize}
	var stdin io.Reader
	if input != "" {
		stdin = strings.NewReader(input)
	}
	h, err := b.t.start(cmd, stdin, &out, stderr)
	if err != nil {
		return "", err
	}
	if err := h.wait(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return out.String(), fmt.Errorf("%w: %s", err, msg)
		}
		return out.String(), err
	}
	return out.String(), nil
}

// StreamCommand runs a command and streams its combined output line by line.
// input (if any) is written to stdin first; stdin then stays open until stop
// is called, so scripts can use "read" on stdin to notice cancellation.
// The channel is closed when the command finishes or stop is called. stop is
// idempotent and returns once the reader goroutine has exited.
func (b base) StreamCommand(cmd, input string) (<-chan string, func(), error) {
	pr, pw := io.Pipe()
	stdinR, stdinW := io.Pipe()
	h, err := b.t.start(cmd, io.MultiReader(strings.NewReader(input), stdinR), pw, pw)
	if err != nil {
		stdinW.Close()
		return nil, nil, err
	}

	ch := make(chan string, 64)
	quit := make(chan struct{})
	done := make(chan struct{})

	go func() {
		h.wait() //nolint:errcheck // the exit status is not part of a line stream
		pw.Close()
		stdinW.Close()
	}()

	go func() {
		defer close(done)
		defer close(ch)
		defer pr.Close() // unblock the writer if we stop early
		readLines(pr, func(line string) bool {
			select {
			case ch <- line:
				return true
			case <-quit:
				return false
			}
		})
	}()

	var once sync.Once
	stop := func() {
		once.Do(func() {
			close(quit)
			stdinW.Close() // EOF lets "read"-based stop traps fire
			h.kill()
			pr.Close()
		})
		<-done
	}
	return ch, stop, nil
}

// InteractiveCommand runs a command, streaming combined output and writing
// each line sent on the returned input channel to the command's stdin.
func (b base) InteractiveCommand(cmd string) (<-chan string, chan<- string, func(), error) {
	pr, pw := io.Pipe()
	stdinR, stdinW := io.Pipe()
	h, err := b.t.start(cmd, stdinR, pw, pw)
	if err != nil {
		stdinW.Close()
		return nil, nil, nil, err
	}

	outCh := make(chan string, 64)
	inCh := make(chan string, 16)
	quit := make(chan struct{})
	done := make(chan struct{})

	go func() {
		h.wait() //nolint:errcheck
		pw.Close()
		stdinW.Close()
	}()

	// Forward input lines to stdin until stop. Once the command has exited
	// writes fail; keep draining so senders never block.
	go func() {
		for {
			select {
			case line := <-inCh:
				io.WriteString(stdinW, line+"\n") //nolint:errcheck
			case <-quit:
				return
			}
		}
	}()

	go func() {
		defer close(done)
		defer close(outCh)
		defer pr.Close()
		readLines(pr, func(line string) bool {
			select {
			case outCh <- line:
				return true
			case <-quit:
				return false
			}
		})
	}()

	var once sync.Once
	stop := func() {
		once.Do(func() {
			close(quit)
			stdinW.Close()
			h.kill()
			pr.Close()
		})
		<-done
	}
	return outCh, inCh, stop, nil
}

// Process is a command started with StartCommand: raw stdout is readable as a
// byte stream, stderr is kept separately for error reporting.
type Process struct {
	stdout *io.PipeReader
	stdinW *io.PipeWriter
	h      *handle
	stderr *tailBuffer

	done    chan struct{}
	err     error
	stopped sync.Once
}

// StartCommand starts a command whose stdout is consumed as raw bytes (e.g. an
// archive or dump). input (if any) is written to stdin first; stdin then stays
// open until the process ends or Stop is called.
func (b base) StartCommand(cmd, input string) (*Process, error) {
	pr, pw := io.Pipe()
	stdinR, stdinW := io.Pipe()
	p := &Process{
		stdout: pr,
		stdinW: stdinW,
		stderr: &tailBuffer{max: stderrTailSize},
		done:   make(chan struct{}),
	}
	h, err := b.t.start(cmd, io.MultiReader(strings.NewReader(input), stdinR), pw, p.stderr)
	if err != nil {
		stdinW.Close()
		return nil, err
	}
	p.h = h
	go func() {
		p.err = h.wait()
		pw.Close()
		stdinW.Close()
		close(p.done)
	}()
	return p, nil
}

// Stdout returns the command's standard output stream.
func (p *Process) Stdout() io.Reader { return p.stdout }

// Wait blocks until the command exits. A non-zero exit is reported together
// with the tail of its stderr.
func (p *Process) Wait() error {
	<-p.done
	if p.err == nil {
		return nil
	}
	if msg := strings.TrimSpace(p.stderr.String()); msg != "" {
		return fmt.Errorf("%w: %s", p.err, msg)
	}
	return p.err
}

// Stderr returns the retained tail of the command's stderr.
func (p *Process) Stderr() string { return p.stderr.String() }

// Stop cancels the command (closing stdin, then killing it) and waits for it
// to exit. Safe to call more than once and after the command has finished.
func (p *Process) Stop() {
	p.stopped.Do(func() {
		p.stdinW.Close()
		p.h.kill()
		p.stdout.Close()
	})
	<-p.done
}

// readLines reads r line by line (without the trailing "\n" or "\r\n"),
// calling emit for each until it returns false or r is exhausted. Lines longer
// than maxLineLen are delivered in maxLineLen-sized chunks.
func readLines(r io.Reader, emit func(string) bool) {
	br := bufio.NewReaderSize(r, 64<<10)
	var long []byte
	for {
		frag, err := br.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			long = append(long, frag...)
			for len(long) >= maxLineLen {
				if !emit(string(long[:maxLineLen])) {
					return
				}
				long = long[maxLineLen:]
			}
			continue
		}
		if len(long) > 0 {
			frag = append(long, frag...)
			long = nil
		}
		if len(frag) > 0 {
			line := bytes.TrimSuffix(frag, []byte("\n"))
			line = bytes.TrimSuffix(line, []byte("\r"))
			if !emit(string(line)) {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// lockedBuffer is a bytes.Buffer safe for concurrent writers. Remote sessions
// copy stdout and stderr on separate goroutines, so a shared plain
// bytes.Buffer races and corrupts or truncates the output.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// tailBuffer keeps only the last max bytes written to it. Safe for
// concurrent use.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = append(t.buf[:0], t.buf[len(t.buf)-t.max:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}
