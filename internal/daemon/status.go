package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

// ErrNoDaemon is returned by Status when no pidfile is present.
var ErrNoDaemon = errors.New("daemon not running")

// StatusInfo is the daemon status snapshot returned by Status.
type StatusInfo struct {
	PID       int
	Alive     bool
	Socket    string
	ListenURL string
	Uptime    time.Duration // best-effort, derived from pidfile mtime
	APIOK     bool          // /api/status returned 2xx
}

// Status inspects the local daemon: reads the pidfile, probes the socket,
// optionally hits /api/status. Reports ErrNoDaemon if there's no pidfile
// at all; a stale pidfile (process gone) is reported with Alive=false.
func Status(p Paths) (StatusInfo, error) {
	var info StatusInfo
	info.Socket = p.Socket

	pid, err := ReadPID(p.Pidfile)
	if err != nil {
		if os.IsNotExist(err) {
			return info, ErrNoDaemon
		}
		return info, err
	}
	info.PID = pid
	info.Alive = IsAlive(pid)

	if st, serr := os.Stat(p.Pidfile); serr == nil {
		info.Uptime = time.Since(st.ModTime()).Truncate(time.Second)
	}

	if !info.Alive {
		return info, nil
	}

	info.ListenURL = "unix://" + p.Socket
	// Probe the API. We use a short-deadline unix-socket dial through a
	// throwaway http.Client.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	hc := &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", p.Socket)
			},
		},
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://unix/api/status", nil)
	resp, err := hc.Do(req)
	if err == nil {
		_ = resp.Body.Close()
		info.APIOK = resp.StatusCode < 400
	}
	return info, nil
}

// PrintStatus writes a human-readable status report to w-equivalent stdout.
func PrintStatus(info StatusInfo) {
	if info.PID == 0 {
		fmt.Println("status:     not running")
		return
	}
	state := "alive"
	if !info.Alive {
		state = "DEAD (stale pidfile)"
	}
	fmt.Printf("pid:        %d (%s)\n", info.PID, state)
	fmt.Printf("socket:     %s\n", info.Socket)
	if info.ListenURL != "" {
		fmt.Printf("listen:     %s\n", info.ListenURL)
	}
	if info.Uptime > 0 {
		fmt.Printf("uptime:     %s\n", info.Uptime)
	}
	if info.Alive {
		if info.APIOK {
			fmt.Println("api:        ok")
		} else {
			fmt.Println("api:        unreachable")
		}
	}
}
