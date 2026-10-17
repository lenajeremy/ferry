package server

import (
	"crypto/x509"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

const testCode = "k7m2q9xa"

func newTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	dir, app := t.TempDir(), t.TempDir()
	store, err := OpenStore(dir, app)
	if err != nil {
		t.Fatal(err)
	}
	store.useTrash = false // tests never touch the real Trash
	thumbs, _ := NewThumbs(filepath.Join(app, "thumbs"))
	web := fstest.MapFS{"index.html": {Data: []byte("<!doctype html><title>Ferry</title>")}, "icon.png": {Data: []byte("png")}}
	host := Host{Name: "Test Mac", LocalName: "test-mac.local", IPs: []net.IP{net.ParseIP("192.168.1.20").To4()}}
	s := NewServer(&Server{store: store, thumbs: thumbs, host: host, code: testCode, port: 7070, folder: "~/Ferry", web: web})
	s.refresh()
	return s, dir
}

// do sends a request as if from addr (the Mac itself is 127.0.0.1 or 192.168.1.20).
func do(s *Server, method, target, addr string, body io.Reader, edit func(*http.Request)) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, body)
	r.RemoteAddr = addr + ":50000"
	r.Host = "localhost:7070"
	if !strings.HasPrefix(addr, "127.") {
		r.Host = "test-mac.local:7070"
	}
	if method != http.MethodGet {
		r.Header.Set("X-Ferry", "1")
	}
	if edit != nil {
		edit(r)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func paired(r *http.Request) { r.AddCookie(&http.Cookie{Name: cookieName, Value: testCode}) }

func TestThisMacNeedsNoCode(t *testing.T) {
	s, _ := newTestServer(t)
	for _, addr := range []string{"127.0.0.1", "192.168.1.20"} {
		if w := do(s, "GET", "/api/files", addr, nil, nil); w.Code != 200 {
			t.Errorf("from %s: %d, want 200", addr, w.Code)
		}
	}
}

func TestOtherDevicesNeedTheCode(t *testing.T) {
	s, _ := newTestServer(t)
	phone := "192.168.0.50"
	if w := do(s, "GET", "/api/files", phone, nil, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("unpaired API: %d, want 401", w.Code)
	}
	if w := do(s, "GET", "/", phone, nil, nil); w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "Connect to Test Mac") {
		t.Fatalf("unpaired page: %d, want the pairing page", w.Code)
	}
	if w := do(s, "GET", "/api/files", phone, nil, paired); w.Code != 200 {
		t.Fatalf("paired: %d, want 200", w.Code)
	}
	// The QR code's link sets the cookie and takes the code out of the address.
	w := do(s, "GET", "/?k="+testCode, phone, nil, nil)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/" {
		t.Fatalf("link: %d to %q, want 303 to /", w.Code, w.Header().Get("Location"))
	}
	c := w.Result().Cookies()
	if len(c) != 1 || c[0].Value == testCode || len(c[0].Value) < 40 || !c[0].HttpOnly {
		t.Fatalf("link cookie: %+v, want the device's own token", c)
	}
	if w := do(s, "GET", "/api/files", phone, nil, func(r *http.Request) { r.AddCookie(c[0]) }); w.Code != 200 {
		t.Fatalf("with its token: %d", w.Code)
	}
	// A wrong code in the link sets nothing.
	if w := do(s, "GET", "/?k=wrongcode", phone, nil, nil); len(w.Result().Cookies()) != 0 {
		t.Fatal("a wrong code set a cookie")
	}
}

// pairApp pairs as the app does and gives back its reply.
func pairApp(t *testing.T, s *Server, code string) (PairReply, int) {
	t.Helper()
	body := strings.NewReader(`{"code":"` + code + `","name":"Test iPhone","kind":"iPhone"}`)
	w := do(s, "POST", "/api/pair", "192.168.0.50", body, func(r *http.Request) { r.Header.Set("Content-Type", "application/json") })
	var reply PairReply
	json.NewDecoder(w.Body).Decode(&reply)
	return reply, w.Code
}

func bearer(token string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set("Authorization", "Ferry "+token) }
}

func TestTheAppTradesTheCodeForAToken(t *testing.T) {
	s, _ := newTestServer(t)
	s.id, s.tlsPort, s.caDER = "comp1", 7071, []byte("authority")
	reply, status := pairApp(t, s, "K7M2 Q9XA")
	if status != 200 || len(reply.Token) < 40 || reply.ID != "comp1" || reply.Name != "Test Mac" || reply.TLSPort != 7071 || reply.CA == "" {
		t.Fatalf("pairing: %d %+v", status, reply)
	}
	if w := do(s, "GET", "/api/files", "192.168.0.50", nil, bearer(reply.Token)); w.Code != 200 {
		t.Fatalf("with the token: %d", w.Code)
	}
	// The code itself no longer signs anything in.
	if w := do(s, "GET", "/api/files", "192.168.0.50", nil, bearer(testCode)); w.Code != http.StatusUnauthorized {
		t.Fatalf("with the code: %d, want 401", w.Code)
	}
	if _, status := pairApp(t, s, "aaaabbbb"); status != http.StatusUnauthorized {
		t.Fatalf("wrong code: %d", status)
	}
}

func TestRemovingADeviceSignsItOut(t *testing.T) {
	s, _ := newTestServer(t)
	phone, _ := pairApp(t, s, testCode)
	other, _ := pairApp(t, s, testCode)
	w := do(s, "GET", "/api/devices", "127.0.0.1", nil, nil)
	var list []DeviceView
	json.NewDecoder(w.Body).Decode(&list)
	if len(list) != 2 || list[0].Name != "Test iPhone" || !list[0].App {
		t.Fatalf("devices: %+v", list)
	}
	// Only this computer sees or changes the list.
	if w := do(s, "GET", "/api/devices", "192.168.0.50", nil, bearer(phone.Token)); w.Code != http.StatusNotFound {
		t.Fatalf("a phone listing devices: %d", w.Code)
	}
	if w := do(s, "DELETE", "/api/devices/"+phone.DeviceID, "192.168.0.50", nil, bearer(other.Token)); w.Code != http.StatusNotFound {
		t.Fatalf("a phone removing another: %d", w.Code)
	}
	if w := do(s, "DELETE", "/api/devices/"+phone.DeviceID, "127.0.0.1", nil, nil); w.Code != http.StatusNoContent {
		t.Fatalf("removing: %d", w.Code)
	}
	if w := do(s, "GET", "/api/files", "192.168.0.50", nil, bearer(phone.Token)); w.Code != http.StatusUnauthorized {
		t.Fatalf("removed device: %d, want 401", w.Code)
	}
	if w := do(s, "GET", "/api/files", "192.168.0.50", nil, bearer(other.Token)); w.Code != 200 {
		t.Fatalf("the other device: %d", w.Code)
	}
}

func TestANewCodeKeepsDevicesPaired(t *testing.T) {
	s, _ := newTestServer(t)
	phone, _ := pairApp(t, s, testCode)
	if w := do(s, "POST", "/api/code", "192.168.0.50", nil, bearer(phone.Token)); w.Code != http.StatusNotFound {
		t.Fatalf("a phone changing the code: %d", w.Code)
	}
	if w := do(s, "POST", "/api/code", "127.0.0.1", nil, nil); w.Code != http.StatusNoContent {
		t.Fatalf("new code: %d", w.Code)
	}
	if s.Code() == testCode || len(s.Code()) != 8 {
		t.Fatalf("the code is still %q", s.Code())
	}
	if _, status := pairApp(t, s, testCode); status != http.StatusUnauthorized {
		t.Fatalf("the old code still pairs: %d", status)
	}
	if w := do(s, "GET", "/api/files", "192.168.0.50", nil, bearer(phone.Token)); w.Code != 200 {
		t.Fatalf("a paired phone after the new code: %d", w.Code)
	}
}

func TestABrowserPairedWithTheOldCookieMovesToAToken(t *testing.T) {
	s, _ := newTestServer(t)
	w := do(s, "GET", "/api/files", "192.168.0.50", nil, paired)
	c := w.Result().Cookies()
	if w.Code != 200 || len(c) != 1 || c[0].Value == testCode {
		t.Fatalf("old cookie: %d %+v", w.Code, c)
	}
	if n := len(s.devices.List()); n != 1 || s.devices.List()[0].Name != "Browser on Device" {
		t.Fatalf("devices: %+v", s.devices.List())
	}
	// A Home Screen app that opens with the code again, already paired, isn't paired twice.
	w = do(s, "GET", "/?k="+testCode, "192.168.0.50", nil, func(r *http.Request) { r.AddCookie(c[0]) })
	if w.Code != http.StatusSeeOther || len(w.Result().Cookies()) != 0 || len(s.devices.List()) != 1 {
		t.Fatalf("again with the code: %d, devices %d", w.Code, len(s.devices.List()))
	}
}

func TestTheQRCodeCarriesTheFingerprint(t *testing.T) {
	s, _ := newTestServer(t)
	s.tlsPort, s.caDER = 7071, []byte("authority")
	byName, _ := s.phoneURLs()
	u, err := url.Parse(byName)
	// The phone apps compute the same: base64url SHA-256, first 22 characters.
	if err != nil || u.Query().Get("k") != testCode || u.Query().Get("f") != "j3b9UBu2jvcfTidrwo8pvO" || u.Query().Get("s") != "7071" {
		t.Fatalf("link: %s", byName)
	}
	// A browser following it is left without the code or the fingerprint in its address.
	w := do(s, "GET", u.RequestURI(), "192.168.0.50", nil, nil)
	if w.Header().Get("Location") != "/" {
		t.Fatalf("redirect: %q", w.Header().Get("Location"))
	}
}

func TestPairingByTypingTheCode(t *testing.T) {
	s, _ := newTestServer(t)
	form := func(code string) func(*http.Request) {
		return func(r *http.Request) { r.Header.Set("Content-Type", "application/x-www-form-urlencoded") }
	}
	w := do(s, "POST", "/pair", "192.168.0.50", strings.NewReader(url.Values{"code": {"K7M2 Q9XA"}}.Encode()), form(""))
	if w.Code != http.StatusSeeOther || len(w.Result().Cookies()) != 1 {
		t.Fatalf("right code (any case, with a space): %d", w.Code)
	}
	w = do(s, "POST", "/pair", "192.168.0.50", strings.NewReader(url.Values{"code": {"aaaabbbb"}}.Encode()), form(""))
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "Check the one on your computer") {
		t.Fatalf("wrong code: %d", w.Code)
	}
}

// A website pointing its own name at 127.0.0.1 (DNS rebinding) isn't this Mac.
func TestAnotherSitesNameIsNotThisMac(t *testing.T) {
	s, _ := newTestServer(t)
	w := do(s, "GET", "/api/files", "127.0.0.1", nil, func(r *http.Request) { r.Host = "evil.example:7070" })
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("%d, want 401", w.Code)
	}
}

func TestChangesNeedFerrysHeaderAndOrigin(t *testing.T) {
	s, _ := newTestServer(t)
	w := do(s, "PUT", "/api/upload?name=a.txt", "127.0.0.1", strings.NewReader("x"), func(r *http.Request) { r.Header.Del("X-Ferry") })
	if w.Code != http.StatusForbidden {
		t.Fatalf("no header: %d, want 403", w.Code)
	}
	w = do(s, "PUT", "/api/upload?name=a.txt", "127.0.0.1", strings.NewReader("x"), func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") })
	if w.Code != http.StatusForbidden {
		t.Fatalf("other origin: %d, want 403", w.Code)
	}
	w = do(s, "PUT", "/api/upload?name=a.txt", "127.0.0.1", strings.NewReader("x"), func(r *http.Request) { r.Header.Set("Origin", "http://localhost:7070") })
	if w.Code != 200 {
		t.Fatalf("same origin: %d, want 200 (%s)", w.Code, w.Body)
	}
}

func TestUploadsGetSafeFreeNames(t *testing.T) {
	s, dir := newTestServer(t)
	upload := func(name, body string) string {
		w := do(s, "PUT", "/api/upload?name="+url.QueryEscape(name)+"&device=iPhone", "192.168.0.50", strings.NewReader(body), paired)
		if w.Code != 200 {
			t.Fatalf("upload %q: %d %s", name, w.Code, w.Body)
		}
		var saved string
		for _, part := range strings.Split(w.Body.String(), ",") {
			if strings.HasPrefix(part, `{"name":`) {
				saved = strings.Trim(strings.TrimPrefix(part, `{"name":`), `"`)
			}
		}
		return saved
	}
	if got := upload("notes.txt", "one"); got != "notes.txt" {
		t.Errorf("first: %q", got)
	}
	if got := upload("notes.txt", "two"); got != "notes 2.txt" {
		t.Errorf("second: %q, want notes 2.txt", got)
	}
	if got := upload("../../escape.txt", "x"); got != "escape.txt" {
		t.Errorf("path: %q, want escape.txt", got)
	}
	if got := upload(".hidden", "x"); got != "hidden" {
		t.Errorf("hidden: %q, want hidden", got)
	}
	if got := upload("image.JPG", "x"); !strings.HasPrefix(got, "Photo ") || !strings.HasSuffix(got, ".jpg") {
		t.Errorf("iPhone's generic name: %q, want Photo <time>.jpg", got)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "notes 2.txt")); string(b) != "two" {
		t.Errorf("notes 2.txt holds %q", b)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escape.txt")); err == nil {
		t.Error("a file escaped the folder")
	}
	// Who sent it is kept.
	for _, e := range s.hub.currentFiles() {
		if e.Name == "notes.txt" && e.From != "iPhone" {
			t.Errorf("notes.txt from %q, want iPhone", e.From)
		}
	}
	if parts, _ := filepath.Glob(filepath.Join(dir, partPrefix+"*")); len(parts) != 0 {
		t.Errorf("part files left behind: %v", parts)
	}
}

func TestOnlySharedFilesAreServed(t *testing.T) {
	s, dir := newTestServer(t)
	os.WriteFile(filepath.Join(dir, "ok.txt"), []byte("fine"), 0o644)
	os.WriteFile(filepath.Join(dir, ".secret"), []byte("no"), 0o644)
	os.WriteFile(filepath.Join(filepath.Dir(dir), "outside.txt"), []byte("no"), 0o644)
	if w := do(s, "GET", "/f/ok.txt", "192.168.0.50", nil, paired); w.Code != 200 || w.Body.String() != "fine" {
		t.Fatalf("shared file: %d %q", w.Code, w.Body)
	}
	for _, p := range []string{"/f/.secret", "/f/..%2Foutside.txt", "/f/%2E%2E%2Foutside.txt"} {
		if w := do(s, "GET", p, "192.168.0.50", nil, paired); w.Code == 200 {
			t.Errorf("%s was served", p)
		}
	}
	// Pages never render from Ferry's address.
	os.WriteFile(filepath.Join(dir, "page.html"), []byte("<script>alert(1)</script>"), 0o644)
	w := do(s, "GET", "/f/page.html", "192.168.0.50", nil, paired)
	if ct := w.Header().Get("Content-Type"); ct != "application/octet-stream" || !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment") {
		t.Errorf("html served as %q, %q", ct, w.Header().Get("Content-Disposition"))
	}
}

func TestVideosCanBeSeekedThrough(t *testing.T) {
	s, dir := newTestServer(t)
	os.WriteFile(filepath.Join(dir, "clip.mov"), []byte("0123456789"), 0o644)
	w := do(s, "GET", "/f/clip.mov", "192.168.0.50", nil, func(r *http.Request) { paired(r); r.Header.Set("Range", "bytes=2-5") })
	if w.Code != http.StatusPartialContent || w.Body.String() != "2345" || w.Header().Get("Content-Type") != "video/quicktime" {
		t.Fatalf("range: %d %q %q", w.Code, w.Body, w.Header().Get("Content-Type"))
	}
}

func TestDelete(t *testing.T) {
	s, dir := newTestServer(t)
	os.WriteFile(filepath.Join(dir, "gone.txt"), []byte("x"), 0o644)
	s.refresh()
	if w := do(s, "DELETE", "/api/files/gone.txt", "192.168.0.50", nil, paired); w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", w.Code)
	}
	if _, err := os.Stat(filepath.Join(dir, "gone.txt")); err == nil {
		t.Fatal("still there")
	}
	if w := do(s, "DELETE", "/api/files/..%2Fx", "192.168.0.50", nil, paired); w.Code != http.StatusNotFound {
		t.Fatalf("delete outside: %d, want 404", w.Code)
	}
}

func TestQRAndCodeOnlyForThisMac(t *testing.T) {
	s, _ := newTestServer(t)
	if w := do(s, "GET", "/qr.svg", "192.168.0.50", nil, paired); w.Code != http.StatusNotFound {
		t.Fatalf("phone got the QR: %d", w.Code)
	}
	if w := do(s, "GET", "/qr.svg", "127.0.0.1", nil, nil); w.Code != 200 || !strings.HasPrefix(w.Body.String(), "<svg") {
		t.Fatalf("Mac QR: %d", w.Code)
	}
}

func TestAuthorityOnlyVouchesForTheHomeNetwork(t *testing.T) {
	ca, err := LoadOrCreateAuthority(t.TempDir(), "Test Mac")
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca.Cert)
	leaf, err := ca.Issue([]string{"test-mac.local", "localhost"}, []net.IP{net.ParseIP("192.168.1.20"), net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"test-mac.local", "192.168.1.20", "localhost"} {
		if _, err := leaf.Leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: name}); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if d := leaf.Leaf.NotAfter.Sub(leaf.Leaf.NotBefore).Hours() / 24; d > 398 {
		t.Errorf("valid for %.0f days; Apple allows 398", d)
	}
	// Even if asked, it can't make a certificate for a real website that anything accepts.
	bad, err := ca.Issue([]string{"bank.example.com"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bad.Leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: "bank.example.com"}); err == nil {
		t.Error("a certificate for bank.example.com verified")
	}
}
