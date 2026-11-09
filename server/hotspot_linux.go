//go:build linux

package server

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Direct Wi-Fi on Linux: a NetworkManager hotspot (nmcli), which shares no internet unless there is some.
// Most laptops' Wi-Fi can't be a network and on one at once, so starting it leaves the Wi-Fi the computer
// is on; the page says so first (hotspotWarning). NetworkManager keeps it up on its own, so Ferry watches
// for it going off, and takes down one left from a Ferry that didn't get to stop it.
const linuxHotspot = "ferry-direct"

func init() {
	hotspotAvailable = func() bool { return wifiDevice() != "" }
	hotspotWarning = func() string {
		if name := wifiNetwork(); name != "" {
			return fmt.Sprintf("This computer leaves %q while it's on.", name)
		}
		return ""
	}
	startHotspot = startLinuxHotspot
	cleanUpHotspot = func() {
		nmcli("connection", "down", linuxHotspot)
		nmcli("connection", "delete", linuxHotspot)
	}
}

func nmcli(args ...string) (string, error) {
	if _, err := exec.LookPath("nmcli"); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "nmcli", args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// wifiDevice is the computer's Wi-Fi card, if NetworkManager runs it ("wlan0").
func wifiDevice() string {
	out, err := nmcli("-t", "-f", "DEVICE,TYPE", "device")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(out, "\n") {
		if dev, kind, _ := strings.Cut(line, ":"); kind == "wifi" {
			return dev
		}
	}
	return ""
}

// wifiNetwork is the Wi-Fi network the computer is on, if any.
func wifiNetwork() string {
	out, _ := nmcli("-t", "-f", "TYPE,STATE,CONNECTION", "device")
	for _, line := range strings.Split(out, "\n") {
		if f := strings.SplitN(line, ":", 3); len(f) == 3 && f[0] == "wifi" && f[1] == "connected" && f[2] != linuxHotspot {
			return f[2]
		}
	}
	return ""
}

func startLinuxHotspot(ssid, password string) (hotspotRun, error) {
	nmcli("connection", "delete", linuxHotspot)
	if out, err := nmcli("device", "wifi", "hotspot", "con-name", linuxHotspot, "ssid", ssid, "password", password); err != nil {
		if out == "" {
			out = err.Error()
		}
		return hotspotRun{}, fmt.Errorf("%s", out)
	}
	// Its address (NetworkManager shares 10.42.0.1).
	ip := ""
	for i := 0; i < 30 && ip == ""; i++ {
		for _, a := range lanIPs() {
			if isHotspotAddr(a) {
				ip = a.String()
			}
		}
		if ip == "" {
			time.Sleep(200 * time.Millisecond)
		}
	}
	if ip == "" {
		ip = "10.42.0.1"
	}
	ended := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(ended)
		for {
			select {
			case <-done:
				return
			case <-time.After(5 * time.Second):
			}
			if state, _ := nmcli("-t", "-g", "GENERAL.STATE", "connection", "show", linuxHotspot); state != "activated" {
				return
			}
		}
	}()
	stop := func() {
		close(done)
		nmcli("connection", "down", linuxHotspot)
		nmcli("connection", "delete", linuxHotspot)
	}
	return hotspotRun{ssid: ssid, password: password, ip: ip, stop: stop, ended: ended}, nil
}
