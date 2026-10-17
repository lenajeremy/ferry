package server

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PairReply is what an app gets for the right code: its token, and how to reach this computer
// securely from now on (the secure port, and Ferry's certificate authority to trust for it).
type PairReply struct {
	Token    string `json:"token"`
	DeviceID string `json:"deviceId"`
	ID       string `json:"id"`   // this computer's id
	Name     string `json:"name"` // this computer's name
	TLSPort  int    `json:"tlsPort,omitempty"`
	CA       string `json:"ca,omitempty"` // base64 DER
}

// pairApp trades the code for a token: POST /api/pair {"code", "name", "kind"}. A device pairing again
// sends its old token ("previous") so its old entry goes; another computer also offers a key to
// itself ("computer"), so the pairing works both ways.
func (s *Server) pairApp(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code, Name, Kind, Previous string
		Computer                   *computerOffer
	}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req)
	} else {
		req.Code, req.Name, req.Kind = r.FormValue("code"), r.FormValue("name"), r.FormValue("kind")
	}
	if !s.codeMatches(req.Code) {
		pairFailures.Lock()
		time.Sleep(time.Second)
		pairFailures.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "That code doesn't match. Check the one on your computer."})
		return
	}
	if old, ok := s.devices.Find(req.Previous); ok {
		s.unpairDevice(old.ID)
	}
	kind := cleanLabel(req.Kind, deviceFromAgent(r.UserAgent()))
	dev, token, err := s.devices.add(cleanLabel(req.Name, kind), kind, true, req.Computer != nil)
	if err != nil {
		log.Printf("saving devices: %v", err)
	}
	if req.Computer != nil {
		s.acceptComputer(r, dev, req.Computer)
	} else {
		log.Printf("paired %s (the app)", dev.Name)
	}
	s.devicesChanged()
	reply := PairReply{Token: token, DeviceID: dev.ID, ID: s.id, Name: s.h().Name}
	if s.tlsPort > 0 && s.caDER != nil {
		reply.TLSPort, reply.CA = s.tlsPort, base64.StdEncoding.EncodeToString(s.caDER)
	}
	writeJSON(w, reply)
}

// DeviceView is a paired device as this computer's page lists it.
type DeviceView struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Kind     string    `json:"kind"`
	App      bool      `json:"app"`
	Computer bool      `json:"computer,omitempty"`
	Paired   time.Time `json:"paired"`
	LastSeen time.Time `json:"lastSeen"`
	Online   bool      `json:"online"`
}

func (s *Server) listDevices(w http.ResponseWriter, r *http.Request) {
	if !s.local(r) {
		http.NotFound(w, r)
		return
	}
	online := map[string]bool{}
	s.hub.mu.Lock()
	for c := range s.hub.clients {
		online[c.deviceID] = true
	}
	s.hub.mu.Unlock()
	out := []DeviceView{}
	for _, d := range s.devices.List() {
		out = append(out, DeviceView{ID: d.ID, Name: d.Name, Kind: d.Kind, App: d.App, Computer: d.Computer, Paired: d.Paired, LastSeen: d.LastSeen, Online: online[d.ID]})
	}
	writeJSON(w, out)
}

// removeDevice unpairs a device and closes its open pages and apps' connections.
func (s *Server) removeDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.local(r) {
		http.NotFound(w, r)
		return
	}
	if !s.unpairDevice(id) {
		http.NotFound(w, r)
		return
	}
	log.Printf("removed a device")
	s.devicesChanged()
	w.WriteHeader(http.StatusNoContent)
}

// removeSelf lets a phone or computer unpair itself (it forgot this computer): DELETE /api/devices/self.
func (s *Server) removeSelf(w http.ResponseWriter, r *http.Request) {
	d, ok := requestDevice(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.unpairDevice(d.ID)
	log.Printf("%s unpaired itself", d.Name)
	s.devicesChanged()
	w.WriteHeader(http.StatusNoContent)
}

// unpairDevice removes a device: its token stops working, its open connections close, and a
// computer it belonged to is forgotten here too.
func (s *Server) unpairDevice(id string) bool {
	if id == "" || !s.devices.Remove(id) {
		return false
	}
	s.hub.mu.Lock()
	for c := range s.hub.clients {
		if c.deviceID == id && c.kick != nil {
			close(c.kick)
			c.kick = nil
		}
	}
	s.hub.mu.Unlock()
	if gone := s.peers.Remove(func(p Peer) bool { return p.DeviceID == id }); len(gone) > 0 {
		s.computersChanged()
	}
	return true
}

// newCode replaces the pairing code. Devices already paired keep their tokens, so nobody is
// disconnected; the old code just stops pairing anything new.
func (s *Server) newCode(w http.ResponseWriter, r *http.Request) {
	if !s.local(r) {
		http.NotFound(w, r)
		return
	}
	code, err := makeCode()
	if err == nil && s.appDir != "" {
		err = os.WriteFile(filepath.Join(s.appDir, "code"), []byte(code), 0o600)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.codeP.Store(&code)
	log.Printf("made a new pairing code")
	s.hub.mu.Lock()
	s.hub.broadcastLocal("code", map[string]string{})
	s.hub.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// devicesChanged tells this computer's open pages to list the devices again.
func (s *Server) devicesChanged() {
	s.hub.mu.Lock()
	s.hub.broadcastLocal("devices", map[string]string{})
	s.hub.mu.Unlock()
}

func (h *hub) broadcastLocal(name string, v any) {
	b, _ := json.Marshal(v)
	for c := range h.clients {
		if !c.local {
			continue
		}
		select {
		case c.ch <- event{name, b}:
		default:
		}
	}
}
