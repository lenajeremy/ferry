//go:build !darwin

package server

import (
	"context"
	"errors"
	"net"
	"net/http"
)

// peerToPeer: Apple's peer-to-peer Wi-Fi is the Mac's (p2p_darwin.go).
func peerToPeer(context.Context, *http.Server, string, string, string, string) {}

func p2pHelper() string { return "" }

func dialPeerToPeer(string) (net.Conn, error) {
	return nil, errors.New("peer-to-peer Wi-Fi is the Mac's")
}
