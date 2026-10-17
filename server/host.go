package server

import (
	"net"
	"os"
	"runtime"
	"sort"
	"strings"
)

var lanIsRemote = os.Getenv("FERRY_LAN_IS_REMOTE") == "1"

// Host is this Mac as other devices on the network reach it.
type Host struct {
	Name      string   // "Alex's MacBook Pro", as shown on the phone
	LocalName string   // "Alexs-MacBook-Pro.local", found by phones through Bonjour
	IPs       []net.IP // its private IPv4 addresses, Wi-Fi/Ethernet first
	PreferIP  bool     // links by address rather than by .local name (Windows, Linux)
}

func DiscoverHost() Host {
	name, local := computerNames()
	h := Host{Name: name, IPs: lanIPs()}
	if local != "" {
		h.LocalName = local + ".local"
	}
	// Phones find a Mac by its .local name; other systems don't always answer to theirs, so their
	// QR code carries the address.
	h.PreferIP = runtime.GOOS != "darwin"
	return h
}

func lanIPs() []net.IP {
	type found struct {
		iface string
		ip    net.IP
	}
	var all []found
	ifaces, _ := net.Interfaces()
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok || n.IP.To4() == nil || !n.IP.IsPrivate() {
				continue
			}
			all = append(all, found{ifc.Name, n.IP.To4()})
		}
	}
	// Wi-Fi and Ethernet first (en0 on a Mac, wl*/eth*/en* on Linux, "Wi-Fi"/"Ethernet" on Windows);
	// containers, virtual machines and VPNs last.
	rank := func(name string) int {
		n := strings.ToLower(name)
		for _, virtual := range []string{"docker", "veth", "br-", "virbr", "vmnet", "vbox", "utun", "tailscale", "zt", "wsl", "hyper-v", "vethernet", "bridge"} {
			if strings.Contains(n, virtual) {
				return 3
			}
		}
		switch {
		case n == "en0":
			return 0
		case strings.HasPrefix(n, "wl") || strings.HasPrefix(n, "wi-fi") || strings.HasPrefix(n, "wifi"):
			return 1
		case strings.HasPrefix(n, "en") || strings.HasPrefix(n, "eth") || strings.HasPrefix(n, "ethernet"):
			return 1
		}
		return 2
	}
	sort.SliceStable(all, func(i, j int) bool { return rank(all[i].iface) < rank(all[j].iface) })
	var ips []net.IP
	for _, f := range all {
		ips = append(ips, f.ip)
	}
	return ips
}

// isSelf reports whether ip is this Mac (loopback, or one of its own addresses).
// FERRY_LAN_IS_REMOTE=1 treats its network address as another device, to preview the phone's
// page in the Simulator (which reaches the Mac from that address).
func (h Host) isSelf(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	if lanIsRemote {
		return false
	}
	for _, own := range h.IPs {
		if own.Equal(ip) {
			return true
		}
	}
	return false
}

// deviceFromAgent names a device from its browser's user agent.
func deviceFromAgent(ua string) string {
	switch {
	case strings.Contains(ua, "iPhone"):
		return "iPhone"
	case strings.Contains(ua, "iPad"):
		return "iPad"
	case strings.Contains(ua, "Android"):
		return "Android"
	case strings.Contains(ua, "Macintosh"):
		return "Mac"
	case strings.Contains(ua, "Windows"):
		return "PC"
	case strings.Contains(ua, "Linux"):
		return "Linux"
	}
	return "Device"
}
