//go:build !windows

package gne

import (
	"fmt"
	"net"
	"os"
	"syscall"
)

// gneTLSConn takes over an already-connected TCP socket: it dups fd
// first (os.NewFile would otherwise own — and eventually close — the
// caller's descriptor), then hands the duplicate to net.FileConn,
// which dups it again for its own use. After this returns, the caller
// must close its own fd and stop using it; the returned conn keeps
// the underlying socket alive independently.
func gneTLSConn(fd uintptr) (net.Conn, error) {
	dup, err := syscall.Dup(int(fd))
	if err != nil {
		return nil, fmt.Errorf("socket dup: %v", err)
	}
	f := os.NewFile(uintptr(dup), "gne-tls")
	if f == nil {
		syscall.Close(dup)
		return nil, fmt.Errorf("socket dup: invalid descriptor")
	}
	conn, err := net.FileConn(f)
	f.Close() // closes our duplicate; FileConn owns its own copy
	if err != nil {
		return nil, fmt.Errorf("socket wrap: %v", err)
	}
	return conn, nil
}
