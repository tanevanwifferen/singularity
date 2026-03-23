package engine

import (
	"io"
	"os"
	"sync"

	"github.com/creack/pty"
)

// PTYProxy implements bubbletea's ExecCommand interface.
// It proxies I/O between the real terminal and an agent's PTY,
// allowing the user to "attach" to a running agent.
// The detach sequence is Ctrl+] (0x1d).
type PTYProxy struct {
	ptmx     *os.File
	stdin    io.Reader
	stdout   io.Writer
	stderr   io.Writer
	done     <-chan struct{} // agent done signal
	onDetach func()         // called when proxy stops (to resume background capture)
}

// NewPTYProxy creates a proxy for the given PTY file descriptor.
// done should be the agent's Done() channel.
// onDetach is called when the proxy finishes (user detaches or agent exits).
func NewPTYProxy(ptmx *os.File, done <-chan struct{}, onDetach func()) *PTYProxy {
	return &PTYProxy{
		ptmx:     ptmx,
		done:     done,
		onDetach: onDetach,
	}
}

// SetStdin sets the terminal's stdin reader.
func (p *PTYProxy) SetStdin(r io.Reader) {
	p.stdin = r
}

// SetStdout sets the terminal's stdout writer.
func (p *PTYProxy) SetStdout(w io.Writer) {
	p.stdout = w
}

// SetStderr sets the terminal's stderr writer.
func (p *PTYProxy) SetStderr(w io.Writer) {
	p.stderr = w
}

// Run blocks, proxying I/O between the terminal and the PTY.
// It returns when the user presses the detach key (Ctrl+]) or the agent exits.
func (p *PTYProxy) Run() error {
	stop := make(chan struct{})
	var once sync.Once
	doStop := func() { once.Do(func() { close(stop) }) }

	// Always call onDetach when we're done to resume background capture
	defer func() {
		if p.onDetach != nil {
			p.onDetach()
		}
	}()

	// Copy PTY output -> terminal stdout
	go func() {
		buf := make([]byte, 4096)
		for {
			select {
			case <-stop:
				return
			default:
			}
			n, err := p.ptmx.Read(buf)
			if n > 0 {
				p.stdout.Write(buf[:n])
			}
			if err != nil {
				doStop()
				return
			}
		}
	}()

	// Copy terminal stdin -> PTY, watching for detach (Ctrl+])
	go func() {
		buf := make([]byte, 256)
		for {
			select {
			case <-stop:
				return
			default:
			}
			n, err := p.stdin.Read(buf)
			if n > 0 {
				for i := 0; i < n; i++ {
					if buf[i] == 0x1d { // Ctrl+]
						doStop()
						return
					}
				}
				p.ptmx.Write(buf[:n])
			}
			if err != nil {
				doStop()
				return
			}
		}
	}()

	// Wait for detach, agent exit, or I/O error
	select {
	case <-stop:
	case <-p.done:
	}

	return nil
}

// ResizePTY resizes the PTY to the given dimensions.
func ResizePTY(ptmx *os.File, rows, cols int) error {
	return pty.Setsize(ptmx, &pty.Winsize{
		Rows: uint16(rows),
		Cols: uint16(cols),
	})
}
