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
	IPs       []net.IP // its IPv4 addresses, Wi-Fi/Ethernet first
	PreferIP  bool     // links by address rather than by .local name (Windows, Linux)
	// Tailnet is its Tailscale address, how paired devices reach it away from home ("" without Tailscale).
	// It isn't one of IPs: Ferry's certificate authority can only vouch for home-network addresses
	// (certs.go), and it mustn't count as this computer's own in isSelf.
	Tailnet string
}

func DiscoverHost() Host {
	name, local := computerNames()
	h := Host{Name: name, IPs: lanIPs(), Tailnet: tailnetAddress()}
	if local != "" {
		h.LocalName = local + ".local"
	}
	// Phones find a Mac by its .local name; other systems don't always answer to theirs, so their
	// QR code carries the address.
	h.PreferIP = runtime.GOOS != "darwin"
	return h
}

// lanIPs are the IPv4 addresses other devices reach this computer by: its private addresses (a
// router's network, a phone's hotspot, a cable to a phone), and on its real network cards the
// 169.254 addresses devices give themselves when there's nothing handing addresses out (two
// computers on a cable, say).
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
			if !ok || n.IP.To4() == nil {
				continue
			}
			if ip := n.IP.To4(); ip.IsPrivate() || ip.IsLinkLocalUnicast() && !isVirtual(ifc.Name) {
				all = append(all, found{ifc.Name, ip})
			}
		}
	}
	// Wi-Fi and Ethernet first (en0 on a Mac, wl*/eth*/en* on Linux, "Wi-Fi"/"Ethernet" on Windows);
	// 169.254 addresses after the rest; containers, virtual machines and VPNs last.
	rank := func(f found) int {
		n := strings.ToLower(f.iface)
		switch {
		case isVirtual(n):
			return 4
		case f.ip.IsLinkLocalUnicast():
			return 3
		case n == "en0":
			return 0
		case strings.HasPrefix(n, "wl") || strings.HasPrefix(n, "wi-fi") || strings.HasPrefix(n, "wifi"):
			return 1
		case strings.HasPrefix(n, "en") || strings.HasPrefix(n, "eth") || strings.HasPrefix(n, "ethernet"):
			return 1
		}
		return 2
	}
	sort.SliceStable(all, func(i, j int) bool { return rank(all[i]) < rank(all[j]) })
	var ips []net.IP
	for _, f := range all {
		ips = append(ips, f.ip)
	}
	return ips
}

// tailnetAddress is this computer's address on its Tailscale network, or "".
func tailnetAddress() string {
	found := ""
	ifaces, _ := net.Interfaces()
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok || !isTailnet(ifc.Name, n.IP) {
				continue
			}
			if strings.Contains(strings.ToLower(ifc.Name), "tailscale") {
				return n.IP.String() // Linux and Windows name it; a Mac's is one of several utun tunnels
			}
			if found == "" {
				found = n.IP.String()
			}
		}
	}
	return found
}

// isTailnet: Tailscale's range (100.64.0.0/10) on a tunnel. Only on a tunnel, because a real network card
// can get an address in that range from a provider that shares its addresses among customers.
func isTailnet(iface string, ip net.IP) bool {
	v4 := ip.To4()
	return v4 != nil && v4[0] == 100 && v4[1]&0xC0 == 64 && isVirtual(iface)
}

// isVirtual: a container's, virtual machine's or VPN's network, not a real network card.
func isVirtual(iface string) bool {
	n := strings.ToLower(iface)
	for _, virtual := range []string{"docker", "veth", "br-", "virbr", "vmnet", "vbox", "utun", "tailscale", "zt", "wsl", "hyper-v", "vethernet", "bridge"} {
		if strings.Contains(n, virtual) {
			return true
		}
	}
	return false
}

// isHotspotAddr reports whether ip is on a computer's own direct Wi-Fi: Windows' (192.168.137.x)
// or NetworkManager's on Linux (10.42.0.x).
func isHotspotAddr(ip net.IP) bool {
	v4 := ip.To4()
	return v4 != nil && (v4[0] == 192 && v4[1] == 168 && v4[2] == 137 || v4[0] == 10 && v4[1] == 42 && v4[2] == 0)
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
