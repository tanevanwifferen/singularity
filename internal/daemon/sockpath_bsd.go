//go:build darwin || dragonfly || freebsd || netbsd || openbsd

package daemon

// maxSocketPath is sizeof(sockaddr_un.sun_path) on the BSDs and macOS,
// where the field is declared char sun_path[104]. The kernel copies the
// path in including its terminating NUL, so the longest usable path is
// maxSocketPath-1 bytes.
const maxSocketPath = 104
