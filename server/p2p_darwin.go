//go:build darwin

package server

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// peerToPeer puts Ferry's secure address on Apple's peer-to-peer Wi-Fi (awdl0, what AirDrop uses), so an
// iPhone nearby reaches this Mac with no network in common. Only Network framework can take connections
// there, so ferry-p2p (desktop/mac-p2p, beside this program) listens and announces, and hands each
// connection over a Unix socket in Ferry's data folder. Those come from no address Ferry knows, so they're
// never "this Mac": like any device's, their requests need a paired device's key. Without ferry-p2p,
// nothing changes. It runs until ctx ends, and is started again if it stops.
func peerToPeer(ctx context.Context, secure *http.Server, dataDir, name, id, fingerprint string) {
	helper := p2pHelper()
	if helper == "" {
		return
	}
	sock := filepath.Join(dataDir, "p2p.sock")
	if len(sock) >= 100 { // a socket's path has to be short (104 bytes on macOS)
		dir, err := os.MkdirTemp("", "ferry")
		if err != nil {
			return
		}
		sock = filepath.Join(dir, "p2p.sock")
	}
	os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		log.Printf("Ferry won't be on peer-to-peer Wi-Fi: %v", err)
		return
	}
	os.Chmod(sock, 0o600)
	go func() {
		if err := secure.ServeTLS(ln, "", ""); !errors.Is(err, http.ErrServerClosed) {
			log.Printf("peer-to-peer Wi-Fi stopped: %v", err)
		}
	}()
	go func() {
		defer os.Remove(sock)
		for ctx.Err() == nil {
			cmd := exec.CommandContext(ctx, helper, name, id, fingerprint, sock)
			alive, _ := cmd.StdinPipe() // closes when Ferry quits, and ferry-p2p with it
			cmd.Stderr = log.Writer()
			if err := cmd.Start(); err != nil {
				log.Printf("Ferry won't be on peer-to-peer Wi-Fi: %v", err)
				return
			}
			cmd.Wait()
			if alive != nil {
				alive.Close()
			}
			select {
			case <-ctx.Done():
			case <-time.After(10 * time.Second):
			}
		}
	}()
}

// p2pHelper is ferry-p2p beside this program (or beside the program a link runs, as Homebrew's do), or "".
func p2pHelper() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	dirs := []string{filepath.Dir(exe)}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		dirs = append(dirs, filepath.Dir(real))
	}
	for _, dir := range dirs {
		if helper := filepath.Join(dir, "ferry-p2p"); fileExists(helper) {
			return helper
		}
	}
	return ""
}

// dialPeerToPeer connects to the paired Mac with this id over Apple's peer-to-peer Wi-Fi, through
// `ferry-p2p connect`, whose standard input and output carry the connection (TLS goes over it, pinned to
// that Mac's authority as always).
func dialPeerToPeer(id string) (net.Conn, error) {
	helper := p2pHelper()
	if helper == "" {
		return nil, errors.New("no ferry-p2p beside Ferry")
	}
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		inR.Close()
		inW.Close()
		return nil, err
	}
	cmd := exec.Command(helper, "connect", id)
	cmd.Stdin, cmd.Stdout = inR, outW
	err = cmd.Start()
	inR.Close()
	outW.Close()
	if err != nil {
		inW.Close()
		outR.Close()
		return nil, err
	}
	return &helperConn{cmd: cmd, r: outR, w: inW}, nil
}

// helperConn is a connection carried by a program's standard output (r) and input (w).
type helperConn struct {
	cmd  *exec.Cmd
	r, w *os.File
}

func (c *helperConn) Read(b []byte) (int, error)  { return c.r.Read(b) }
func (c *helperConn) Write(b []byte) (int, error) { return c.w.Write(b) }
func (c *helperConn) Close() error {
	c.w.Close()
	c.r.Close()
	c.cmd.Process.Kill()
	go c.cmd.Wait()
	return nil
}
func (c *helperConn) LocalAddr() net.Addr  { return peerToPeerAddr{} }
func (c *helperConn) RemoteAddr() net.Addr { return peerToPeerAddr{} }
func (c *helperConn) SetDeadline(t time.Time) error {
	c.r.SetDeadline(t)
	return c.w.SetDeadline(t)
}
func (c *helperConn) SetReadDeadline(t time.Time) error  { return c.r.SetDeadline(t) }
func (c *helperConn) SetWriteDeadline(t time.Time) error { return c.w.SetDeadline(t) }

type peerToPeerAddr struct{}

func (peerToPeerAddr) Network() string { return "peer-to-peer" }
func (peerToPeerAddr) String() string  { return peerToPeerHost }
