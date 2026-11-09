package server

import (
	"crypto/rand"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"sync"

	"ferry/qr"
)

// Direct Wi-Fi: on Windows and Linux, Ferry can start a Wi-Fi network of the computer's own, with no
// router and no internet (hotspot_windows.go, hotspot_linux.go). Phones join it by scanning a code on
// the computer's page, then reach Ferry as on any network. A Mac can't start one for an app; iPhones
// reach a Mac nearby over Apple's peer-to-peer Wi-Fi instead (p2p_darwin.go).

// HotspotState is the computer's direct Wi-Fi, for its own page (never sent to other devices).
type HotspotState struct {
	Supported bool   `json:"supported"` // this computer can start one (Windows, Linux with NetworkManager)
	PeerP2P   bool   `json:"peerP2P"`   // a Mac: iPhones nearby reach it over peer-to-peer Wi-Fi
	Active    bool   `json:"active"`
	SSID      string `json:"ssid,omitempty"`
	Password  string `json:"password,omitempty"`
	IP        string `json:"ip,omitempty"`
	// Warning is what starting it would do besides (Linux: drop the Wi-Fi it's on).
	Warning string `json:"warning,omitempty"`
}

// hotspotRun is a direct Wi-Fi network that's on.
type hotspotRun struct {
	ssid, password, ip string
	stop               func()          // turns it off
	ended              <-chan struct{} // closed if it goes off by itself
}

// The system's direct Wi-Fi (set by hotspot_windows.go and hotspot_linux.go; tests replace them).
var (
	hotspotAvailable = func() bool { return false }
	hotspotWarning   = func() string { return "" }
	startHotspot     = func(ssid, password string) (hotspotRun, error) {
		return hotspotRun{}, fmt.Errorf("direct Wi-Fi isn't available on %s", runtime.GOOS)
	}
	// cleanUpHotspot takes down one a Ferry that didn't get to stop it left on (Linux keeps it).
	cleanUpHotspot = func() {}
)

type hotspotManager struct {
	mu       sync.Mutex
	starting bool
	run      *hotspotRun
}

func newHotspotManager() *hotspotManager { return &hotspotManager{} }

func (h *hotspotManager) state() HotspotState {
	h.mu.Lock()
	run := h.run
	h.mu.Unlock()
	st := HotspotState{Supported: hotspotAvailable(), PeerP2P: runtime.GOOS == "darwin"}
	if run != nil {
		st.Active, st.SSID, st.Password, st.IP = true, run.ssid, run.password, run.ip
	} else if st.Supported {
		st.Warning = hotspotWarning()
	}
	return st
}

// start turns direct Wi-Fi on, named after the computer. changed is called if it goes off by itself.
func (h *hotspotManager) start(name string, changed func()) error {
	h.mu.Lock()
	if h.run != nil {
		h.mu.Unlock()
		return nil
	}
	if h.starting {
		h.mu.Unlock()
		return fmt.Errorf("direct Wi-Fi is already starting")
	}
	h.starting = true
	h.mu.Unlock()

	password, err := wifiPassword()
	if err == nil {
		var run hotspotRun
		run, err = startHotspot("Ferry-"+hotspotLabel(name), password)
		if err == nil {
			h.mu.Lock()
			h.run = &run
			h.mu.Unlock()
			go func() {
				<-run.ended
				h.mu.Lock()
				ours := h.run == &run
				if ours {
					h.run = nil
				}
				h.mu.Unlock()
				if ours {
					changed()
				}
			}()
		}
	}
	h.mu.Lock()
	h.starting = false
	h.mu.Unlock()
	return err
}

func (h *hotspotManager) stop() {
	h.mu.Lock()
	run := h.run
	h.run = nil
	h.mu.Unlock()
	if run != nil {
		run.stop()
	}
}

// hotspotLabel is the computer's name as a network name may have it: letters, digits and dashes, short
// enough for "Ferry-" in front (32 bytes at most).
func hotspotLabel(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case (r == ' ' || r == '-' || r == '_') && b.Len() > 0 && !strings.HasSuffix(b.String(), "-"):
			b.WriteByte('-')
		}
		if b.Len() >= 26 {
			break
		}
	}
	if s := strings.Trim(b.String(), "-"); s != "" {
		return s
	}
	return "PC"
}

// wifiPassword is 12 characters a person can read off a screen (about 59 bits).
func wifiPassword() (string, error) {
	const alphabet = "abcdefghjkmnpqrstuvwxyz23456789"
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	for i, b := range raw {
		raw[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(raw), nil
}

// wifiQRString is a QR code's way of offering a Wi-Fi network (WIFI:…;;), which the iPhone's and
// Android's cameras join in a tap.
func wifiQRString(ssid, password string) string {
	esc := strings.NewReplacer(`\`, `\\`, `;`, `\;`, `,`, `\,`, `:`, `\:`, `"`, `\"`).Replace
	return fmt.Sprintf("WIFI:T:WPA;S:%s;P:%s;;", esc(ssid), esc(password))
}

// MARK: The computer's page

func (s *Server) getHotspot(w http.ResponseWriter, r *http.Request) {
	if !s.local(r) {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, s.hotspot.state())
}

func (s *Server) startHotspot(w http.ResponseWriter, r *http.Request) {
	if !s.local(r) {
		http.NotFound(w, r)
		return
	}
	if err := s.hotspot.start(s.h().Name, s.hotspotChanged); err != nil {
		jsonError(w, http.StatusBadGateway, "Couldn't start direct Wi-Fi: "+err.Error())
		return
	}
	s.hotspotChanged()
	writeJSON(w, s.hotspot.state())
}

func (s *Server) stopHotspot(w http.ResponseWriter, r *http.Request) {
	if !s.local(r) {
		http.NotFound(w, r)
		return
	}
	s.hotspot.stop()
	s.hotspotChanged()
	writeJSON(w, s.hotspot.state())
}

func (s *Server) wifiQR(w http.ResponseWriter, r *http.Request) {
	if !s.local(r) {
		http.NotFound(w, r)
		return
	}
	st := s.hotspot.state()
	if !st.Active {
		http.NotFound(w, r)
		return
	}
	code, err := qr.Encode(wifiQRString(st.SSID, st.Password))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprint(w, code.SVG("#111111", "#ffffff"))
}

// hotspotChanged: the computer's addresses change with its direct Wi-Fi (a new certificate covers them),
// and its page shows it.
func (s *Server) hotspotChanged() {
	s.refreshNetworkNow()
	s.hub.mu.Lock()
	s.hub.broadcastLocal("hotspot", s.hotspot.state())
	s.hub.mu.Unlock()
}
