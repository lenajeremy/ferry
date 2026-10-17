package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Peer is another computer this one is paired with, both ways: each has a key to the other, so
// either can send to the other and look through its files. They talk over the secure address only,
// trusting nothing but the other's own certificate authority.
type Peer struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Host     string    `json:"host"` // an address
	Port     int       `json:"port"`
	TLSPort  int       `json:"tlsPort"`
	CA       []byte    `json:"ca"`
	Token    string    `json:"token"`    // this computer's key there
	DeviceID string    `json:"deviceId"` // the key it has here, in devices
	Paired   time.Time `json:"paired"`
}

func (p Peer) base() string {
	return "https://" + net.JoinHostPort(p.Host, strconv.Itoa(p.TLSPort))
}

// Peers are kept in peers.json in Ferry's data folder (in memory only when path is "").
type Peers struct {
	mu       sync.Mutex
	path     string
	list     []Peer
	clients  map[string]*http.Client
	problems map[string]string // by id: why it last refused (it removed this computer)
}

func OpenPeers(dir string) (*Peers, error) {
	p := &Peers{path: filepath.Join(dir, "peers.json")}
	b, err := os.ReadFile(p.path)
	if errors.Is(err, os.ErrNotExist) {
		return p, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &p.list); err != nil {
		return nil, fmt.Errorf("peers.json: %w", err)
	}
	return p, nil
}

func (ps *Peers) Get(id string) (Peer, bool) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	for _, p := range ps.list {
		if p.ID == id {
			return p, true
		}
	}
	return Peer{}, false
}

func (ps *Peers) List() []Peer {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return slices.Clone(ps.list)
}

// Put adds a peer, or replaces the one with its id.
func (ps *Peers) Put(p Peer) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.list = slices.DeleteFunc(ps.list, func(x Peer) bool { return x.ID == p.ID })
	ps.list = append(ps.list, p)
	delete(ps.clients, p.ID)
	delete(ps.problems, p.ID)
	ps.saveLocked()
}

// Remove forgets a peer by its id, or by the key it has here.
func (ps *Peers) Remove(match func(Peer) bool) []Peer {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	var gone []Peer
	ps.list = slices.DeleteFunc(ps.list, func(p Peer) bool {
		if match(p) {
			gone = append(gone, p)
			delete(ps.clients, p.ID)
			return true
		}
		return false
	})
	if len(gone) > 0 {
		ps.saveLocked()
	}
	return gone
}

// moved notes a paired computer's new address, as it announces it.
func (ps *Peers) moved(id, host string) bool {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	for i := range ps.list {
		if ps.list[i].ID == id && ps.list[i].Host != host {
			ps.list[i].Host = host
			delete(ps.clients, id)
			ps.saveLocked()
			return true
		}
	}
	return false
}

func (ps *Peers) setProblem(id, problem string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if ps.problems == nil {
		ps.problems = map[string]string{}
	}
	ps.problems[id] = problem
}

func (ps *Peers) problem(id string) string {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return ps.problems[id]
}

func (ps *Peers) saveLocked() {
	if ps.path == "" {
		return
	}
	b, _ := json.MarshalIndent(ps.list, "", "  ")
	tmp := ps.path + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		os.Rename(tmp, ps.path)
	}
}

// client talks to a peer's secure address, trusting only its certificate authority.
func (ps *Peers) client(p Peer) *http.Client {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if c := ps.clients[p.ID]; c != nil {
		return c
	}
	if ps.clients == nil {
		ps.clients = map[string]*http.Client{}
	}
	c := pinnedClient(p.CA)
	ps.clients[p.ID] = c
	return c
}

func pinnedClient(caDER []byte) *http.Client {
	pool := x509.NewCertPool()
	if ca, err := x509.ParseCertificate(caDER); err == nil {
		pool.AddCert(ca)
	}
	return &http.Client{Transport: &http.Transport{
		TLSClientConfig:       &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		DialContext:           (&net.Dialer{Timeout: 8 * time.Second}).DialContext,
		TLSHandshakeTimeout:   8 * time.Second,
		ResponseHeaderTimeout: 2 * time.Minute,
		IdleConnTimeout:       90 * time.Second,
	}}
}

// localKind is what this computer is, as other devices list it.
func localKind() string {
	switch runtime.GOOS {
	case "darwin":
		return "Mac"
	case "windows":
		return "PC"
	}
	return "Linux"
}

func fingerprintOf(der []byte) string {
	sum := sha256.Sum256(der)
	return base64.RawURLEncoding.EncodeToString(sum[:])[:22]
}

// MARK: Pairing with another computer

// ComputerView is another computer as this computer's page lists it: paired, or nearby to pair with.
type ComputerView struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Host    string `json:"host"`
	Paired  bool   `json:"paired"`
	Nearby  bool   `json:"nearby"`
	Problem string `json:"problem,omitempty"`
}

func (s *Server) listComputers(w http.ResponseWriter, r *http.Request) {
	if !s.local(r) {
		http.NotFound(w, r)
		return
	}
	nearby := map[string]Nearby{}
	if s.finder != nil {
		for _, n := range s.finder.list() {
			nearby[n.ID] = n
			s.peers.moved(n.ID, n.Host)
		}
	}
	out := []ComputerView{}
	for _, p := range s.peers.List() {
		_, near := nearby[p.ID]
		out = append(out, ComputerView{ID: p.ID, Name: p.Name, Host: p.Host, Paired: true, Nearby: near, Problem: s.peers.problem(p.ID)})
		delete(nearby, p.ID)
	}
	slices.SortFunc(out, func(a, b ComputerView) int { return strings.Compare(a.Name, b.Name) })
	var rest []ComputerView
	for _, n := range nearby {
		rest = append(rest, ComputerView{ID: n.ID, Name: n.Name, Host: n.Host, Nearby: true})
	}
	slices.SortFunc(rest, func(a, b ComputerView) int { return strings.Compare(a.Name, b.Name) })
	writeJSON(w, append(out, rest...))
}

// pairComputer pairs with another computer, given its code: one found on the network (id), or one
// typed in (address). The code goes only over the other's secure address, to the certificate
// authority it announces (checked against its fingerprint). This computer hands over a key of its
// own in the same request, so the pairing works both ways.
func (s *Server) pairComputer(w http.ResponseWriter, r *http.Request) {
	if !s.local(r) {
		http.NotFound(w, r)
		return
	}
	var req struct{ ID, Address, Code string }
	json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req)
	var target Nearby
	if req.ID != "" {
		n, ok := s.finder.byID(req.ID)
		if !ok {
			jsonError(w, http.StatusNotFound, "That computer isn't on the network any more.")
			return
		}
		target = n
	} else {
		host, port, err := splitAddress(req.Address)
		if err != nil {
			jsonError(w, http.StatusBadRequest, "That address doesn't look right. Use the one shown on the other computer, like 192.168.1.20.")
			return
		}
		target = Nearby{Name: host, Host: host, Port: port}
	}
	if s.caDER == nil || s.tlsPort == 0 {
		jsonError(w, http.StatusServiceUnavailable, "This computer has no secure address to pair with.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	ca, tlsPort, err := fetchAuthority(ctx, target.Host, target.Port)
	if err != nil {
		jsonError(w, http.StatusBadGateway, fmt.Sprintf("Couldn't reach Ferry at %s. Is it running there, on the same network?", target.Host))
		return
	}
	if target.Fingerprint != "" && fingerprintOf(ca) != target.Fingerprint {
		jsonError(w, http.StatusBadGateway, "Something answered for that computer that isn't it. Try again on your own network.")
		return
	}
	if target.TLSPort != 0 {
		tlsPort = target.TLSPort
	}
	if tlsPort == 0 {
		jsonError(w, http.StatusBadGateway, "That computer's Ferry needs updating before computers can pair.")
		return
	}

	dev, tokenForThem, _ := s.devices.add(target.Name, "Computer", true, true)
	offer := map[string]any{
		"code": req.Code, "name": s.h().Name, "kind": localKind(),
		"computer": map[string]any{"id": s.id, "name": s.h().Name, "host": localAddressFor(target.Host), "port": s.port,
			"tlsPort": s.tlsPort, "ca": base64.StdEncoding.EncodeToString(s.caDER), "token": tokenForThem},
	}
	if old, ok := s.peers.Get(req.ID); ok {
		offer["previous"] = old.Token // replace this computer's old entry there
	}
	body, _ := json.Marshal(offer)
	call, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+net.JoinHostPort(target.Host, strconv.Itoa(tlsPort))+"/api/pair", bytes.NewReader(body))
	call.Header.Set("Content-Type", "application/json")
	call.Header.Set("X-Ferry", "1")
	resp, err := pinnedClient(ca).Do(call)
	if err != nil {
		log.Printf("pairing with %s: %v", target.Host, err)
		s.devices.Remove(dev.ID)
		jsonError(w, http.StatusBadGateway, "Couldn't connect securely to that computer.")
		return
	}
	defer resp.Body.Close()
	var reply PairReply
	if resp.StatusCode == http.StatusUnauthorized {
		s.devices.Remove(dev.ID)
		jsonError(w, http.StatusUnauthorized, "That code doesn't match. Check the one on the other computer.")
		return
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&reply) != nil || reply.ID == "" {
		s.devices.Remove(dev.ID)
		jsonError(w, http.StatusBadGateway, "That computer's Ferry needs updating before computers can pair.")
		return
	}
	s.devices.Rename(dev.ID, reply.Name)
	if old, ok := s.peers.Get(reply.ID); ok && old.DeviceID != dev.ID {
		s.devices.Remove(old.DeviceID)
	}
	s.peers.Put(Peer{ID: reply.ID, Name: reply.Name, Host: target.Host, Port: target.Port, TLSPort: tlsPort, CA: ca,
		Token: reply.Token, DeviceID: dev.ID, Paired: time.Now().UTC().Truncate(time.Second)})
	log.Printf("paired with %s", reply.Name)
	s.computersChanged()
	s.devicesChanged()
	writeJSON(w, ComputerView{ID: reply.ID, Name: reply.Name, Host: target.Host, Paired: true})
}

// acceptComputer keeps what a computer pairing with this one offered: a key to it, so this
// computer can send there too.
func (s *Server) acceptComputer(r *http.Request, dev Device, offer *computerOffer) {
	if offer == nil || offer.ID == "" || offer.ID == s.id || offer.Token == "" || offer.TLSPort == 0 {
		return
	}
	ca, err := base64.StdEncoding.DecodeString(offer.CA)
	if err != nil {
		return
	}
	// Reach it the way it reached us, when that's on the local network.
	host := offer.Host
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		if ip := net.ParseIP(h); ip != nil && (ip.IsPrivate() || ip.IsLoopback()) {
			host = ip.String()
		}
	}
	if old, ok := s.peers.Get(offer.ID); ok && old.DeviceID != dev.ID {
		s.devices.Remove(old.DeviceID)
	}
	s.peers.Put(Peer{ID: offer.ID, Name: cleanLabel(offer.Name, "Computer"), Host: host, Port: offer.Port, TLSPort: offer.TLSPort,
		CA: ca, Token: offer.Token, DeviceID: dev.ID, Paired: time.Now().UTC().Truncate(time.Second)})
	log.Printf("paired with %s", offer.Name)
	s.computersChanged()
}

type computerOffer struct {
	ID, Name, Host string
	Port, TLSPort  int
	CA, Token      string
}

// forgetComputer unpairs another computer: its key here stops working, and it's asked to forget
// this one too.
func (s *Server) forgetComputer(w http.ResponseWriter, r *http.Request) {
	if !s.local(r) {
		http.NotFound(w, r)
		return
	}
	id := r.PathValue("id")
	gone := s.peers.Remove(func(p Peer) bool { return p.ID == id })
	if len(gone) == 0 {
		http.NotFound(w, r)
		return
	}
	p := gone[0]
	s.unpairDevice(p.DeviceID)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, http.MethodDelete, p.base()+"/api/devices/self", nil)
		req.Header.Set("Authorization", "Ferry "+p.Token)
		req.Header.Set("X-Ferry", "1")
		if resp, err := pinnedClient(p.CA).Do(req); err == nil {
			resp.Body.Close()
		}
	}()
	log.Printf("forgot %s", p.Name)
	s.computersChanged()
	s.devicesChanged()
	w.WriteHeader(http.StatusNoContent)
}

// MARK: Sending, looking, fetching

// call makes a request to a paired computer with this computer's key, noting if it refuses it.
func (s *Server) call(ctx context.Context, p Peer, method, path string, body io.Reader, size int64) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, p.base()+path, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.ContentLength = size
		req.Header.Set("Content-Type", "application/octet-stream")
	}
	req.Header.Set("Authorization", "Ferry "+p.Token)
	if method != http.MethodGet {
		req.Header.Set("X-Ferry", "1")
	}
	resp, err := s.peers.client(p).Do(req)
	if err == nil && resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		s.peers.setProblem(p.ID, "removed")
		s.computersChanged()
		return nil, errRemoved
	}
	return resp, err
}

var errRemoved = errors.New("removed")

func (s *Server) peerFor(w http.ResponseWriter, r *http.Request) (Peer, bool) {
	if !s.local(r) {
		http.NotFound(w, r)
		return Peer{}, false
	}
	p, ok := s.peers.Get(r.PathValue("id"))
	if !ok {
		jsonError(w, http.StatusNotFound, "That computer isn't paired any more.")
	}
	return p, ok
}

func (s *Server) peerError(w http.ResponseWriter, p Peer, err error) {
	if errors.Is(err, errRemoved) {
		jsonError(w, http.StatusConflict, fmt.Sprintf("%s has removed this computer. Pair with it again.", p.Name))
		return
	}
	jsonError(w, http.StatusBadGateway, fmt.Sprintf("Couldn't reach %s. Is Ferry running there?", p.Name))
}

// sendToComputer passes a file from this computer's page straight on to a paired computer.
func (s *Server) sendToComputer(w http.ResponseWriter, r *http.Request) {
	p, ok := s.peerFor(w, r)
	if !ok {
		return
	}
	name := r.URL.Query().Get("name")
	from := s.h().Name
	if len([]rune(from)) > 32 {
		from = string([]rune(from)[:32])
	}
	resp, err := s.call(r.Context(), p, http.MethodPut, "/api/upload?name="+url.QueryEscape(name)+"&device="+url.QueryEscape(from), r.Body, r.ContentLength)
	if err != nil {
		s.peerError(w, p, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		log.Printf("sent %s to %s", name, p.Name)
	}
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// computerFiles lists a paired computer's files; computerThumb passes on their thumbnails.
func (s *Server) computerFiles(w http.ResponseWriter, r *http.Request) {
	s.passOn(w, r, "/api/files")
}

func (s *Server) computerThumb(w http.ResponseWriter, r *http.Request) {
	s.passOn(w, r, "/thumb/"+url.PathEscape(r.PathValue("name"))+"?v="+url.QueryEscape(r.URL.Query().Get("v")))
}

func (s *Server) passOn(w http.ResponseWriter, r *http.Request, path string) {
	p, ok := s.peerFor(w, r)
	if !ok {
		return
	}
	resp, err := s.call(r.Context(), p, http.MethodGet, path, nil, 0)
	if err != nil {
		s.peerError(w, p, err)
		return
	}
	defer resp.Body.Close()
	for _, h := range []string{"Content-Type", "Content-Length", "Cache-Control"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// fetchFromComputer copies a file from a paired computer into this computer's Ferry folder.
func (s *Server) fetchFromComputer(w http.ResponseWriter, r *http.Request) {
	p, ok := s.peerFor(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	resp, err := s.call(r.Context(), p, http.MethodGet, "/f/"+url.PathEscape(name), nil, 0)
	if err != nil {
		s.peerError(w, p, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		jsonError(w, http.StatusNotFound, fmt.Sprintf("%s doesn't have that file any more.", p.Name))
		return
	}
	e, err := s.store.Save(name, resp.Body, p.Name)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "Couldn't save it: "+err.Error())
		return
	}
	log.Printf("fetched %s from %s", e.Name, p.Name)
	s.refresh()
	writeJSON(w, e)
}

// MARK: Helpers

// fetchAuthority gets another computer's certificate authority from its plain address, with its
// secure port (which it says in a header).
func fetchAuthority(ctx context.Context, host string, port int) ([]byte, int, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+net.JoinHostPort(host, strconv.Itoa(port))+"/ferry.cer", nil)
	resp, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	der, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil || resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("no certificate (%d)", resp.StatusCode)
	}
	if _, err := x509.ParseCertificate(der); err != nil {
		return nil, 0, err
	}
	tlsPort, _ := strconv.Atoi(resp.Header.Get("X-Ferry-TLS-Port"))
	return der, tlsPort, nil
}

// splitAddress reads "192.168.1.20", "studio.local:7070" or a pasted link.
func splitAddress(s string) (string, int, error) {
	s = strings.TrimSpace(s)
	if u, err := url.Parse(s); err == nil && u.Host != "" {
		s = u.Host
	}
	host, portText, err := net.SplitHostPort(s)
	if err != nil {
		host, portText = s, "7070"
	}
	port, err := strconv.Atoi(portText)
	if host == "" || err != nil || port <= 0 || port > 65535 || strings.ContainsAny(host, "/ ") {
		return "", 0, errors.New("bad address")
	}
	return host, port, nil
}

// localAddressFor is this computer's address on the network that reaches host.
func localAddressFor(host string) string {
	if c, err := net.Dial("udp", net.JoinHostPort(host, "9")); err == nil {
		defer c.Close()
		if a, ok := c.LocalAddr().(*net.UDPAddr); ok {
			return a.IP.String()
		}
	}
	return ""
}

func jsonError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func (s *Server) computersChanged() {
	s.hub.mu.Lock()
	s.hub.broadcastLocal("computers", map[string]string{})
	s.hub.mu.Unlock()
}
