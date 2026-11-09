package server

import (
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// computer is a Ferry listening for real (plain and secure), as another computer reaches it.
type computer struct {
	*Server
	dir   string
	plain string // host:port
}

func newComputer(t *testing.T, id, name, code string) computer {
	t.Helper()
	s, dir := newTestServer(t)
	s.id = id
	s.codeP.Store(&code)
	h := Host{Name: name, LocalName: "localhost", IPs: []net.IP{net.IPv4(127, 0, 0, 1)}}
	s.current.Store(&h)
	ca, err := LoadOrCreateAuthority(t.TempDir(), id)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := ca.Issue([]string{"localhost"}, []net.IP{net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	s.ca, s.caDER = ca, ca.Cert.Raw
	s.cert.Store(&cert)
	secure := httptest.NewUnstartedServer(s)
	// (httptest adds a certificate of its own unless given one, and serves it to addresses without a name.)
	secure.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	secure.StartTLS()
	plain := httptest.NewServer(s)
	t.Cleanup(func() { secure.Close(); plain.Close() })
	u, _ := url.Parse(plain.URL)
	su, _ := url.Parse(secure.URL)
	s.port, _ = strconv.Atoi(u.Port())
	s.tlsPort, _ = strconv.Atoi(su.Port())
	return computer{s, dir, u.Host}
}

func (c computer) local(t *testing.T, method, target string, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *httptest.ResponseRecorder
	if body == "" {
		r = do(c.Server, method, target, "127.0.0.1", nil, nil)
	} else {
		r = do(c.Server, method, target, "127.0.0.1", strings.NewReader(body), func(r *http.Request) { r.Header.Set("Content-Type", "application/json") })
	}
	return r
}

func TestComputersPairBothWaysSendAndForget(t *testing.T) {
	mac := newComputer(t, "mac1", "MacBook Pro", "macmac22")
	pc := newComputer(t, "pc1", "Studio PC", "pcpcpc33")

	// A wrong code pairs nothing, and leaves no key behind.
	if w := mac.local(t, "POST", "/api/computers/pair", `{"address":"`+pc.plain+`","code":"wrongcod"}`); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong code: %d %s", w.Code, w.Body)
	}
	if len(mac.devices.List()) != 0 || len(pc.peers.List()) != 0 {
		t.Fatal("a failed pairing left something behind")
	}

	// The Mac types the PC's code: both now have each other, over the secure address.
	if w := mac.local(t, "POST", "/api/computers/pair", `{"address":"`+pc.plain+`","code":"pcpc pc33"}`); w.Code != 200 {
		t.Fatalf("pairing: %d %s", w.Code, w.Body)
	}
	toPC, ok := mac.peers.Get("pc1")
	if !ok || toPC.Name != "Studio PC" || toPC.TLSPort != pc.tlsPort || toPC.Token == "" {
		t.Fatalf("the Mac's peer: %+v", toPC)
	}
	toMac, ok := pc.peers.Get("mac1")
	if !ok || toMac.Name != "MacBook Pro" || toMac.TLSPort != mac.tlsPort || toMac.Host != "127.0.0.1" {
		t.Fatalf("the PC's peer: %+v", toMac)
	}
	if d := pc.devices.List(); len(d) != 1 || !d[0].Computer || d[0].Name != "MacBook Pro" {
		t.Fatalf("the PC's devices: %+v", d)
	}

	// Each sends to the other.
	if w := do(mac.Server, "PUT", "/api/computers/pc1/upload?name=report.txt", "127.0.0.1", strings.NewReader("from the mac"), nil); w.Code != 200 {
		t.Fatalf("mac → pc: %d %s", w.Code, w.Body)
	}
	if b, err := os.ReadFile(filepath.Join(pc.dir, "report.txt")); err != nil || string(b) != "from the mac" {
		t.Fatalf("on the PC: %q %v", b, err)
	}
	if w := do(pc.Server, "PUT", "/api/computers/mac1/upload?name=photo.jpg", "127.0.0.1", strings.NewReader("jpeg"), nil); w.Code != 200 {
		t.Fatalf("pc → mac: %d %s", w.Code, w.Body)
	}
	pc.refresh()

	// The Mac looks through the PC's files and fetches one.
	w := mac.local(t, "GET", "/api/computers/pc1/files", "")
	var files []Entry
	json.NewDecoder(w.Body).Decode(&files)
	if w.Code != 200 || len(files) != 1 || files[0].Name != "report.txt" || files[0].From != "MacBook Pro" {
		t.Fatalf("the PC's files: %d %+v", w.Code, files)
	}
	os.Remove(filepath.Join(mac.dir, "report.txt"))
	if w := mac.local(t, "POST", "/api/computers/pc1/fetch/report.txt", ""); w.Code != 200 {
		t.Fatalf("fetch: %d %s", w.Code, w.Body)
	}
	if b, _ := os.ReadFile(filepath.Join(mac.dir, "report.txt")); string(b) != "from the mac" {
		t.Fatalf("fetched: %q", b)
	}

	// Only this computer's own page uses any of this.
	if w := do(mac.Server, "GET", "/api/computers", "192.168.0.50", nil, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("a stranger listing computers: %d", w.Code)
	}

	// Pairing again replaces the old entries rather than adding to them.
	if w := mac.local(t, "POST", "/api/computers/pair", `{"address":"`+pc.plain+`","code":"pcpcpc33"}`); w.Code != 200 {
		t.Fatalf("pairing again: %d", w.Code)
	}
	if len(pc.devices.List()) != 1 || len(mac.devices.List()) != 1 || len(pc.peers.List()) != 1 {
		t.Fatalf("after pairing again: pc devices %d, mac devices %d", len(pc.devices.List()), len(mac.devices.List()))
	}

	// The Mac forgets the PC: the PC's key on the Mac stops working, and the PC forgets the Mac too.
	if w := mac.local(t, "DELETE", "/api/computers/pc1", ""); w.Code != http.StatusNoContent {
		t.Fatalf("forget: %d", w.Code)
	}
	if len(mac.devices.List()) != 0 {
		t.Fatalf("the PC still has a key on the Mac: %+v", mac.devices.List())
	}
	for i := 0; i < 50 && len(pc.peers.List()) > 0; i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if len(pc.peers.List()) != 0 || len(pc.devices.List()) != 0 {
		t.Fatalf("the PC didn't forget the Mac: peers %+v devices %+v", pc.peers.List(), pc.devices.List())
	}
}

func TestAPhonePairingAgainReplacesItsEntry(t *testing.T) {
	s, _ := newTestServer(t)
	first, _ := pairApp(t, s, testCode)
	body := strings.NewReader(`{"code":"` + testCode + `","name":"Test iPhone","kind":"iPhone","previous":"` + first.Token + `"}`)
	w := do(s, "POST", "/api/pair", "192.168.0.50", body, func(r *http.Request) { r.Header.Set("Content-Type", "application/json") })
	if w.Code != 200 || len(s.devices.List()) != 1 {
		t.Fatalf("pairing again: %d, %d devices", w.Code, len(s.devices.List()))
	}
	// And a phone can unpair itself.
	var reply PairReply
	json.NewDecoder(w.Body).Decode(&reply)
	if w := do(s, "DELETE", "/api/devices/self", "192.168.0.50", nil, bearer(reply.Token)); w.Code != http.StatusNoContent || len(s.devices.List()) != 0 {
		t.Fatalf("unpairing itself: %d", w.Code)
	}
}

func TestAComputerThatMovedIsFoundAgain(t *testing.T) {
	mac := newComputer(t, "mac1", "MacBook Pro", "macmac22")
	pc := newComputer(t, "pc1", "Studio PC", "pcpcpc33")
	if w := mac.local(t, "POST", "/api/computers/pair", `{"address":"`+pc.plain+`","code":"pcpcpc33"}`); w.Code != 200 {
		t.Fatalf("pairing: %d %s", w.Code, w.Body)
	}
	// The PC left where it was (nothing answers there now); Bonjour hears it at its new address.
	mac.peers.moved("pc1", "::1")
	mac.finder.put("Studio PC", Nearby{ID: "pc1", Name: "Studio PC", Host: "127.0.0.1", Port: 1})

	start := time.Now()
	w := mac.local(t, "GET", "/api/computers/pc1/files", "")
	if w.Code != 200 {
		t.Fatalf("looking at the PC's files after it moved: %d %s", w.Code, w.Body)
	}
	if p, _ := mac.peers.Get("pc1"); p.Host != "127.0.0.1" {
		t.Errorf("the PC's address is still %q", p.Host)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("finding it took %v", d)
	}
}

func TestAComputerAwayIsFoundOverTailscale(t *testing.T) {
	mac := newComputer(t, "mac1", "MacBook Pro", "macmac22")
	pc := newComputer(t, "pc1", "Studio PC", "pcpcpc33")
	if w := mac.local(t, "POST", "/api/computers/pair", `{"address":"`+pc.plain+`","code":"pcpcpc33"}`); w.Code != 200 {
		t.Fatalf("pairing: %d %s", w.Code, w.Body)
	}
	// Any answer from the PC tells the Mac its Tailscale address. (127.0.0.1 stands in for one here.)
	h := pc.h()
	h.Tailnet = "127.0.0.1"
	pc.current.Store(&h)
	if w := mac.local(t, "GET", "/api/computers/pc1/files", ""); w.Code != 200 {
		t.Fatalf("looking at the PC's files: %d %s", w.Code, w.Body)
	}
	if p, _ := mac.peers.Get("pc1"); p.Tailnet != "127.0.0.1" {
		t.Fatalf("the Mac kept Tailscale address %q", p.Tailnet)
	}
	// Then the Mac leaves home: nothing answers at the PC's address, and Bonjour hears nothing.
	mac.peers.moved("pc1", "::1")
	if w := mac.local(t, "GET", "/api/computers/pc1/files", ""); w.Code != 200 {
		t.Fatalf("looking at the PC's files from away: %d %s", w.Code, w.Body)
	}
	if p, _ := mac.peers.Get("pc1"); p.Host != "127.0.0.1" {
		t.Errorf("the PC's address is still %q", p.Host)
	}
}

func TestOnlyThatComputerCountsAsFound(t *testing.T) {
	mac := newComputer(t, "mac1", "MacBook Pro", "macmac22")
	pc := newComputer(t, "pc1", "Studio PC", "pcpcpc33")
	other := newComputer(t, "pc2", "Somebody's PC", "otherpc4")
	if w := mac.local(t, "POST", "/api/computers/pair", `{"address":"`+pc.plain+`","code":"pcpcpc33"}`); w.Code != 200 {
		t.Fatalf("pairing: %d %s", w.Code, w.Body)
	}
	p, _ := mac.peers.Get("pc1")
	if err := reach(t.Context(), p, "127.0.0.1"); err != nil {
		t.Fatalf("the PC itself: %v", err)
	}
	// Another computer at the same address (another Ferry, its own authority) isn't the PC, and is
	// never sent the key: relocate only moves on a handshake that the PC's authority vouches for.
	p.TLSPort = other.tlsPort
	if err := reach(t.Context(), p, "127.0.0.1"); err == nil {
		t.Fatal("another computer passed for the PC")
	}
	mac.finder.put("Somebody's PC", Nearby{ID: "pc1", Name: "Studio PC", Host: "127.0.0.1", Port: 1})
	p.Host = "::1"
	if _, ok := mac.relocate(t.Context(), p); ok {
		t.Fatal("moved the PC to another computer's address")
	}
	// And a computer that's gone is looked for at most every 20 seconds.
	start := time.Now()
	if _, ok := mac.relocate(t.Context(), p); ok || time.Since(start) > 100*time.Millisecond {
		t.Errorf("looked again straight away (%v)", time.Since(start))
	}
}

func TestGateways(t *testing.T) {
	got := gateways([]net.IP{net.ParseIP("192.168.0.20"), net.ParseIP("192.168.137.45"), net.ParseIP("10.42.0.1"), net.ParseIP("192.168.2.3")})
	if strings.Join(got, " ") != "192.168.137.1 192.168.2.1" {
		t.Errorf("gateways: %v", got)
	}
}
