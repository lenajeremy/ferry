package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ferry/qr"
)

type Server struct {
	store   *Store
	thumbs  *Thumbs
	host    Host
	code    string // the pairing code at start (codeP holds the current one)
	id      string // this computer's own id
	appDir  string // Ferry's data folder ("" in tests: nothing is written)
	devices *Devices
	peers   *Peers
	finder  *finder
	port    int
	tlsPort int // 0: no secure address
	caDER   []byte
	folder  string // the shared folder, as shown ("~/Ferry")
	web     fs.FS

	mux *http.ServeMux
	hub hub

	// The Mac's name and addresses, looked up again as networks come and go (Ferry may start at
	// login before Wi-Fi has joined), and the secure page's certificate, reissued to match.
	current atomic.Pointer[Host]
	ca      *Authority
	cert    atomic.Pointer[tls.Certificate]
	codeP   atomic.Pointer[string]
}

type deviceKey struct{}

const cookieName = "ferry"

func NewServer(s *Server) *Server {
	s.hub.clients = map[*client]bool{}
	h := s.host
	s.current.Store(&h)
	code := s.code
	s.codeP.Store(&code)
	if s.devices == nil {
		s.devices = &Devices{}
	}
	if s.peers == nil {
		s.peers = &Peers{}
	}
	if s.finder == nil {
		s.finder = newFinder(s.id, func() {})
	}
	m := http.NewServeMux()
	m.HandleFunc("GET /{$}", s.index)
	m.HandleFunc("GET /icon.png", s.static("icon.png", "image/png"))
	m.HandleFunc("GET /manifest.webmanifest", s.manifest)
	m.HandleFunc("GET /api/events", s.events)
	m.HandleFunc("GET /api/files", s.files)
	m.HandleFunc("GET /api/info", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.info(r, s.device(r))) })
	m.HandleFunc("PUT /api/upload", s.upload)
	m.HandleFunc("DELETE /api/files/{name}", s.remove)
	m.HandleFunc("POST /api/reveal/{name}", s.reveal)
	m.HandleFunc("POST /api/open/{name}", s.openFile)
	m.HandleFunc("POST /api/open-folder", s.openFolder)
	m.HandleFunc("GET /f/{name}", s.file)
	m.HandleFunc("GET /thumb/{name}", s.thumb)
	m.HandleFunc("GET /qr.svg", s.qr)
	m.HandleFunc("GET /ferry.cer", s.authority)
	m.HandleFunc("GET /api/devices", s.listDevices)
	m.HandleFunc("DELETE /api/devices/{id}", s.removeDevice)
	m.HandleFunc("DELETE /api/devices/self", s.removeSelf)
	m.HandleFunc("GET /api/computers", s.listComputers)
	m.HandleFunc("POST /api/computers/pair", s.pairComputer)
	m.HandleFunc("DELETE /api/computers/{id}", s.forgetComputer)
	m.HandleFunc("PUT /api/computers/{id}/upload", s.sendToComputer)
	m.HandleFunc("GET /api/computers/{id}/files", s.computerFiles)
	m.HandleFunc("GET /api/computers/{id}/thumb/{name}", s.computerThumb)
	m.HandleFunc("POST /api/computers/{id}/fetch/{name}", s.fetchFromComputer)
	m.HandleFunc("POST /api/code", s.newCode)
	m.HandleFunc("POST /api/pair", s.pairApp)
	s.mux = m
	return s
}

// h is the Mac as it is now.
func (s *Server) h() Host { return *s.current.Load() }

// watchNetwork looks the Mac up again every half minute. A new address gets a new certificate
// for the secure page, so it keeps working by address as well as by name.
func (s *Server) watchNetwork() {
	for range time.Tick(30 * time.Second) {
		now := DiscoverHost()
		before := s.h()
		if fmt.Sprint(now) == fmt.Sprint(before) {
			continue
		}
		s.current.Store(&now)
		log.Printf("network changed: %s, %v", now.LocalName, now.IPs)
		if s.ca != nil {
			if cert, err := s.issue(now); err == nil {
				s.cert.Store(&cert)
			}
		}
	}
}

func (s *Server) issue(h Host) (tls.Certificate, error) {
	ips := append([]net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}, h.IPs...)
	return s.ca.Issue([]string{h.LocalName, "localhost"}, ips)
}

// MARK: Access

// ServeHTTP lets this computer in, and devices that have paired: by scanning the QR code (?k=),
// typing the code, or (the apps) trading the code for a token at /api/pair. Each paired device
// then signs in with its own token: a cookie in a browser, "Authorization: Ferry <token>" in the apps.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")

	dev, isPaired := s.paired(r)
	if k := r.URL.Query().Get("k"); k != "" && r.Method == http.MethodGet {
		// A Home Screen app comes back with the code every time: it pairs only once.
		if s.codeMatches(k) && !isPaired && !s.local(r) {
			s.pairBrowser(w, r)
		}
		// Take the code (and the apps' fingerprint) out of the address, so it isn't left in history.
		q := r.URL.Query()
		q.Del("k")
		q.Del("f")
		q.Del("s")
		u := *r.URL
		u.RawQuery = q.Encode()
		http.Redirect(w, r, u.RequestURI(), http.StatusSeeOther)
		return
	}
	if !isPaired && !s.local(r) {
		// A browser paired before devices had their own tokens still has the code as its cookie.
		if c, err := r.Cookie(cookieName); err == nil && s.codeMatches(c.Value) {
			dev, isPaired = s.pairBrowser(w, r), true
		}
	}
	if !s.local(r) && !isPaired {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/pair":
			s.pair(w, r)
		case r.Method == http.MethodPost && r.URL.Path == "/api/pair":
			s.pairApp(w, r)
		case r.URL.Path == "/icon.png", r.URL.Path == "/ferry.cer":
			s.mux.ServeHTTP(w, r)
		case strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/f/") || strings.HasPrefix(r.URL.Path, "/thumb/"):
			http.Error(w, "not paired", http.StatusUnauthorized)
		default:
			s.pairPage(w, http.StatusUnauthorized, "")
		}
		return
	}
	// Changes need Ferry's own header (which another site's page can't send without asking
	// first) and, when the browser says where the request is from, this address.
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		if r.Header.Get("X-Ferry") != "1" || !sameOrigin(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}
	if isPaired {
		r = r.WithContext(context.WithValue(r.Context(), deviceKey{}, dev))
	}
	s.mux.ServeHTTP(w, r)
}

// local is this Mac itself, reached by a name that is really this Mac (not a website that
// pointed its own name here).
func (s *Server) local(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || !s.h().isSelf(net.ParseIP(host)) {
		return false
	}
	name := r.Host
	if h, _, err := net.SplitHostPort(r.Host); err == nil {
		name = h
	}
	name = strings.ToLower(strings.Trim(name, "[]"))
	if name == "localhost" || name == strings.ToLower(s.h().LocalName) {
		return true
	}
	ip := net.ParseIP(name)
	return ip != nil && s.h().isSelf(ip)
}

// paired is the device a request comes from: by the token in the apps' header or a browser's cookie.
func (s *Server) paired(r *http.Request) (Device, bool) {
	if token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Ferry "); ok {
		return s.devices.Find(token)
	}
	if c, err := r.Cookie(cookieName); err == nil {
		return s.devices.Find(c.Value)
	}
	return Device{}, false
}

// requestDevice is the paired device behind a request that got through ServeHTTP.
func requestDevice(r *http.Request) (Device, bool) {
	d, ok := r.Context().Value(deviceKey{}).(Device)
	return d, ok
}

// Code is the current pairing code.
func (s *Server) Code() string { return *s.codeP.Load() }

func (s *Server) codeMatches(given string) bool {
	given = strings.ToLower(strings.NewReplacer(" ", "", "-", "").Replace(strings.TrimSpace(given)))
	return subtle.ConstantTimeCompare([]byte(given), []byte(s.Code())) == 1
}

// pairBrowser pairs the browser behind r and leaves it its token as a cookie.
func (s *Server) pairBrowser(w http.ResponseWriter, r *http.Request) Device {
	kind := deviceFromAgent(r.UserAgent())
	dev, token, err := s.devices.Add(browserName(r.UserAgent()), kind, false)
	if err != nil {
		log.Printf("saving devices: %v", err)
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: token, Path: "/", MaxAge: 400 * 24 * 3600,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil})
	log.Printf("paired %s", dev.Name)
	s.devicesChanged()
	return dev
}

// fingerprint identifies Ferry's certificate authority in the QR code, so the apps can check
// they reached this computer before sending the code: base64url of SHA-256, first 128 bits.
func (s *Server) fingerprint() string {
	if s.caDER == nil {
		return ""
	}
	sum := sha256.Sum256(s.caDER)
	return base64.RawURLEncoding.EncodeToString(sum[:])[:22]
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && strings.EqualFold(u.Host, r.Host)
}

var pairFailures sync.Mutex

func (s *Server) pair(w http.ResponseWriter, r *http.Request) {
	if s.codeMatches(r.FormValue("code")) {
		s.pairBrowser(w, r)
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	// One wrong guess at a time, slowly.
	pairFailures.Lock()
	time.Sleep(time.Second)
	pairFailures.Unlock()
	s.pairPage(w, http.StatusUnauthorized, "That code doesn't match. Check the one on your computer.")
}

func (s *Server) pairPage(w http.ResponseWriter, status int, problem string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	pairTemplate.Execute(w, map[string]string{"Host": s.h().Name, "Problem": problem})
}

// MARK: Pages

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	b, err := fs.ReadFile(s.web, "index.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(b)
}

func (s *Server) static(name, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := fs.ReadFile(s.web, name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Write(b)
	}
}

// manifest makes "Add to Home Screen" open Ferry already paired: a Home Screen app keeps its
// own cookies, so its start address carries the code.
func (s *Server) manifest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/manifest+json")
	w.Header().Set("Cache-Control", "no-cache")
	json.NewEncoder(w).Encode(map[string]any{
		"name": "Ferry", "short_name": "Ferry", "start_url": "/?k=" + s.Code(), "scope": "/",
		"display": "standalone", "background_color": "#F3F1EC", "theme_color": "#F3F1EC",
		"icons": []map[string]string{{"src": "/icon.png", "sizes": "512x512", "type": "image/png", "purpose": "any"}},
	})
}

// Info is what the page needs to know about where it is and who it's talking to.
type Info struct {
	ID     string `json:"id"` // this computer's id
	Host   string `json:"host"`
	Device string `json:"device"`
	// The paired device asking (absent for this computer itself).
	DeviceID string `json:"deviceId,omitempty"`
	// The secure address's port, for the apps.
	TLSPort int  `json:"tlsPort,omitempty"`
	Local   bool `json:"local"`
	Secure  bool `json:"secure"`
	// SecureURL is the HTTPS address (with the code, as the phone may reach it by another name).
	SecureURL string `json:"secureURL,omitempty"`
	// Only for this Mac: how a phone gets in.
	PhoneURL   string `json:"phoneURL,omitempty"`
	PhoneIPURL string `json:"phoneIPURL,omitempty"`
	Code       string `json:"code,omitempty"`
	Folder     string `json:"folder,omitempty"`
	// How this computer names things: "darwin"/"windows"/"linux", its file manager, its bin.
	OS          string `json:"os"`
	FileManager string `json:"fileManager"`
	Trash       string `json:"trash"`
}

func (s *Server) info(r *http.Request, device string) Info {
	in := Info{ID: s.id, Host: s.h().Name, Device: device, Local: s.local(r), Secure: r.TLS != nil,
		OS: runtime.GOOS, FileManager: fileManager(), Trash: trashName()}
	if d, ok := requestDevice(r); ok {
		in.DeviceID = d.ID
	}
	if s.tlsPort > 0 && s.caDER != nil {
		in.TLSPort = s.tlsPort
		if h := s.h(); h.PreferIP && len(h.IPs) > 0 {
			in.SecureURL = fmt.Sprintf("https://%s:%d/?k=%s", h.IPs[0], s.tlsPort, s.Code())
		} else if h.LocalName != "" {
			in.SecureURL = fmt.Sprintf("https://%s:%d/?k=%s", h.LocalName, s.tlsPort, s.Code())
		}
	}
	if in.Local {
		in.PhoneURL, in.PhoneIPURL = s.phoneURLs()
		code := s.Code()
		in.Code = code[:4] + " " + code[4:]
		in.Folder = s.folder
	}
	return in
}

// phoneURLs: by Bonjour name (keeps working when the Mac's address changes) and by address.
// For the apps, the link also carries the certificate's fingerprint (f) and the secure port (s):
// they check the one against the other before sending the code. Browsers ignore both.
func (s *Server) phoneURLs() (byName, byIP string) {
	extra := ""
	if fp := s.fingerprint(); fp != "" && s.tlsPort > 0 {
		extra = fmt.Sprintf("&f=%s&s=%d", fp, s.tlsPort)
	}
	if s.h().LocalName != "" {
		byName = fmt.Sprintf("http://%s:%d/?k=%s%s", s.h().LocalName, s.port, s.Code(), extra)
	}
	if len(s.h().IPs) > 0 {
		byIP = fmt.Sprintf("http://%s:%d/?k=%s%s", s.h().IPs[0], s.port, s.Code(), extra)
	}
	if byName == "" || s.h().PreferIP && byIP != "" {
		byName = byIP
	}
	return
}

func (s *Server) qr(w http.ResponseWriter, r *http.Request) {
	if !s.local(r) {
		http.NotFound(w, r)
		return
	}
	byName, byIP := s.phoneURLs()
	link := byName
	if r.URL.Query().Get("ip") == "1" && byIP != "" {
		link = byIP
	}
	code, err := qr.Encode(link)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprint(w, code.SVG("#111111", "#ffffff"))
}

func (s *Server) authority(w http.ResponseWriter, r *http.Request) {
	if s.caDER == nil {
		http.NotFound(w, r)
		return
	}
	// Safari offers this type as a profile to install. Other computers learn the secure port here.
	w.Header().Set("Content-Type", "application/x-x509-ca-cert")
	if s.tlsPort > 0 {
		w.Header().Set("X-Ferry-TLS-Port", strconv.Itoa(s.tlsPort))
	}
	w.Header().Set("Content-Disposition", `attachment; filename="Ferry.cer"`)
	w.Write(s.caDER)
}

// MARK: Files

func (s *Server) files(w http.ResponseWriter, r *http.Request) {
	entries, err := s.store.List()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, entries)
}

func (s *Server) device(r *http.Request) string {
	// Another computer is named as itself.
	if d, ok := requestDevice(r); ok && d.Computer {
		return d.Name
	}
	if s.local(r) {
		return localDevice()
	}
	// The page knows better than its user agent (an iPad's Safari says it's a Mac).
	if d := r.URL.Query().Get("device"); d != "" && len(d) <= 32 {
		return d
	}
	return deviceFromAgent(r.UserAgent())
}

func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		http.Error(w, "no file name", http.StatusBadRequest)
		return
	}
	from := s.device(r)
	e, err := s.store.Save(name, r.Body, from)
	if err != nil {
		log.Printf("upload %q from %s: %v", name, from, err)
		http.Error(w, "couldn't save the file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("received %s (%s) from %s", e.Name, humanSize(e.Size), from)
	s.refresh()
	writeJSON(w, e)
}

func (s *Server) remove(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Delete(r.PathValue("name")); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, os.ErrNotExist) {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}
	s.refresh()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) reveal(w http.ResponseWriter, r *http.Request) {
	p, err := s.store.Path(r.PathValue("name"))
	if !s.local(r) || err != nil {
		http.NotFound(w, r)
		return
	}
	revealThing(p)
	w.WriteHeader(http.StatusNoContent)
}

// openFile opens a file on this computer in its usual app (Preview, Photos, VLC, …).
func (s *Server) openFile(w http.ResponseWriter, r *http.Request) {
	p, err := s.store.Path(r.PathValue("name"))
	if !s.local(r) || err != nil {
		http.NotFound(w, r)
		return
	}
	openThing(p)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) openFolder(w http.ResponseWriter, r *http.Request) {
	if !s.local(r) {
		http.NotFound(w, r)
		return
	}
	openThing(s.store.dir)
	w.WriteHeader(http.StatusNoContent)
}

var extraTypes = map[string]string{
	".heic": "image/heic", ".heif": "image/heif", ".mov": "video/quicktime", ".m4v": "video/x-m4v",
	".mp4": "video/mp4", ".m4a": "audio/mp4", ".webp": "image/webp", ".avif": "image/avif",
}

func (s *Server) file(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	p, err := s.store.Path(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(p)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ext := strings.ToLower(filepath.Ext(name))
	ct := extraTypes[ext]
	if ct == "" {
		ct = mime.TypeByExtension(ext)
	}
	if ct == "" {
		ct = "application/octet-stream"
	}
	// Scripts never run from here: a page is only ever a download, and anything opened on its
	// own (an SVG, say) is sandboxed. PDFs are left alone so the browser's viewer works.
	if ct != "image/svg+xml" && (strings.Contains(ct, "html") || strings.Contains(ct, "xml") || strings.Contains(ct, "javascript")) {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	if ct != "application/pdf" {
		w.Header().Set("Content-Security-Policy", "sandbox")
	}
	disposition := "inline"
	if r.URL.Query().Get("dl") == "1" || ct == "application/octet-stream" {
		disposition = "attachment"
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": name}))
	w.Header().Set("Cache-Control", "private, no-cache")
	http.ServeContent(w, r, name, info.ModTime(), f)
}

func (s *Server) thumb(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	p, err := s.store.Path(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	size := 480
	if r.URL.Query().Get("s") == "large" {
		size = 2560
	}
	var entry *Entry
	for _, e := range s.hub.currentFiles() {
		if e.Name == name {
			entry = &e
			break
		}
	}
	if entry == nil {
		http.NotFound(w, r)
		return
	}
	out, err := s.thumbs.Get(p, *entry, size)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	http.ServeFile(w, r, out)
}

// MARK: Live updates

type event struct {
	name string
	data []byte
}

type client struct {
	ch       chan event
	device   string
	deviceID string
	local    bool
	kick     chan struct{} // closed when the device is removed
}

type hub struct {
	mu      sync.Mutex
	clients map[*client]bool
	files   []Entry
	sig     string
}

// Presence is another device with Ferry open.
type Presence struct {
	Device string `json:"device"`
	ID     string `json:"id,omitempty"`
	Local  bool   `json:"local"`
}

func (h *hub) currentFiles() []Entry {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.files
}

func (h *hub) peers() []Presence {
	out := []Presence{}
	for c := range h.clients {
		out = append(out, Presence{Device: c.device, ID: c.deviceID, Local: c.local})
	}
	return out
}

func (h *hub) broadcast(name string, v any) {
	b, _ := json.Marshal(v)
	for c := range h.clients {
		select {
		case c.ch <- event{name, b}:
		default: // a stuck client misses an update; the next one carries everything
		}
	}
}

// refresh reads the folder and tells every open page when anything changed.
func (s *Server) refresh() {
	entries, err := s.store.List()
	if err != nil {
		log.Printf("reading the folder: %v", err)
		return
	}
	sig := Signature(entries)
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	if sig == s.hub.sig {
		return
	}
	if entries == nil {
		entries = []Entry{}
	}
	s.hub.files, s.hub.sig = entries, sig
	s.hub.broadcast("files", entries)
}

// Watch notices files added, renamed or removed in Finder, and network changes.
func (s *Server) Watch() {
	go s.watchNetwork()
	s.refresh()
	for range time.Tick(time.Second) {
		s.refresh()
	}
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")

	c := &client{ch: make(chan event, 16), device: s.device(r), local: s.local(r), kick: make(chan struct{})}
	if d, ok := requestDevice(r); ok {
		c.deviceID = d.ID
	}
	s.hub.mu.Lock()
	s.hub.clients[c] = true
	files := s.hub.files
	if files == nil {
		files = []Entry{}
	}
	hello, _ := json.Marshal(map[string]any{"info": s.info(r, c.device), "files": files})
	s.hub.broadcast("peers", s.hub.peers()) // this page's copy follows the hello
	s.hub.mu.Unlock()
	if !c.local {
		log.Printf("%s connected", c.device)
	}

	defer func() {
		s.hub.mu.Lock()
		delete(s.hub.clients, c)
		s.hub.broadcast("peers", s.hub.peers())
		s.hub.mu.Unlock()
		if !c.local {
			log.Printf("%s left", c.device)
		}
	}()

	fmt.Fprintf(w, "retry: 2000\nevent: hello\ndata: %s\n\n", hello)
	flusher.Flush()
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-c.kick:
			return
		case e := <-c.ch:
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.name, e.data)
			flusher.Flush()
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

// MARK: Helpers

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}

var pairTemplate = template.Must(template.New("pair").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover">
<meta name="theme-color" content="#F3F1EC" media="(prefers-color-scheme: light)">
<meta name="theme-color" content="#111214" media="(prefers-color-scheme: dark)">
<title>Ferry</title><link rel="apple-touch-icon" href="/icon.png"><link rel="icon" href="/icon.png">
<style>
:root{--bg:#F3F1EC;--surface:#fff;--ink:#16171A;--ink2:#61646B;--line:rgba(22,23,26,.12);--accent:#F2542D;color-scheme:light}
@media (prefers-color-scheme:dark){:root{--bg:#111214;--surface:#1C1D20;--ink:#F3F2EF;--ink2:#A2A5AC;--line:rgba(243,242,239,.14);--accent:#FF6B45;color-scheme:dark}}
*{box-sizing:border-box}body{margin:0;min-height:100vh;background:var(--bg);color:var(--ink);font:16px/1.45 -apple-system,BlinkMacSystemFont,"SF Pro Text","Helvetica Neue",sans-serif;display:grid;place-items:center;padding:24px 20px}
main{width:100%;max-width:360px;display:grid;gap:20px}
img{width:64px;height:64px;border-radius:15px}
h1{font-size:28px;line-height:1.15;margin:0;letter-spacing:-.01em;text-wrap:balance}
p{margin:0;color:var(--ink2)}
form{display:grid;gap:12px}
input{font:600 24px/1 ui-monospace,"SF Mono",Menlo,monospace;letter-spacing:.18em;text-align:center;text-transform:lowercase;padding:16px;border-radius:14px;border:1px solid var(--line);background:var(--surface);color:var(--ink);width:100%}
input:focus{outline:2px solid var(--accent);outline-offset:1px}
button{font-family:inherit;font-size:17px;font-weight:600;padding:16px;border:0;border-radius:14px;background:var(--ink);color:var(--bg);cursor:pointer}
.problem{color:var(--accent);font-size:15px}
</style></head><body><main>
<img src="/icon.png" alt="">
<h1>Connect to {{.Host}}</h1>
<p>Scan the QR code on Ferry's page on your computer, or type the code shown under it.</p>
<form method="post" action="/pair">
<input name="code" autocomplete="one-time-code" autocapitalize="none" autocorrect="off" spellcheck="false" placeholder="xxxx xxxx" maxlength="12" required autofocus>
{{if .Problem}}<p class="problem">{{.Problem}}</p>{{end}}
<button type="submit">Connect</button>
</form></main></body></html>`))
