//go:build !(darwin || dragonfly || freebsd || netbsd || openbsd)

package daemon

// maxSocketPath is sizeof(sockaddr_un.sun_path) on Linux and the other
// platforms we build for, where the field is declared char sun_path[108].
// The kernel copies the path in including its terminating NUL, so the
// longest usable path is maxSocketPath-1 bytes.
const maxSocketPath = 108
