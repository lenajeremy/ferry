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
