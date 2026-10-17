// Package server is Ferry: the shared folder, the page that both the computer and the phones use,
// pairing, live updates, thumbnails and the network announcement. The command-line program and the
// desktop app both run it.
package server

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

//go:embed web
var webFiles embed.FS

// Config is how Ferry runs.
type Config struct {
	Dir     string // the shared folder
	DataDir string // Ferry's own data (code, certificate, thumbnails)
	Port    int    // the page
	TLSPort int    // the secure page (0 turns it off)
	Bonjour bool   // announce Ferry on the network for the phone apps
	Name    string // the name the phones see (default: the computer's)
	// Ready is called once Ferry is listening.
	Ready func(*Server)
}

// DefaultConfig: ~/Ferry, the system's data folder, ports 7070 and 7071, announced on the network.
func DefaultConfig() Config {
	home, _ := os.UserHomeDir()
	return Config{Dir: filepath.Join(home, "Ferry"), DataDir: AppDataDir(), Port: 7070, TLSPort: 7071, Bonjour: true}
}

// ErrPortInUse means something (usually Ferry itself) already has the page's port.
var ErrPortInUse = errors.New("port in use")

// Run runs Ferry until ctx ends.
func Run(ctx context.Context, cfg Config) error {
	nameOverride = cfg.Name
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return err
	}
	code, err := accessCode(cfg.DataDir)
	if err != nil {
		return err
	}
	id, err := computerID(cfg.DataDir)
	if err != nil {
		return err
	}
	devices, err := OpenDevices(cfg.DataDir)
	if err != nil {
		return err
	}
	peers, err := OpenPeers(cfg.DataDir)
	if err != nil {
		return err
	}
	store, err := OpenStore(cfg.Dir, cfg.DataDir)
	if err != nil {
		return fmt.Errorf("shared folder: %w", err)
	}
	thumbs, err := NewThumbs(filepath.Join(cfg.DataDir, "thumbnails"))
	if err != nil {
		return err
	}
	go thumbs.Prune()
	host := DiscoverHost()
	web, _ := fs.Sub(webFiles, "web")

	home, _ := os.UserHomeDir()
	shown := cfg.Dir
	if rel, err := filepath.Rel(home, cfg.Dir); err == nil && !strings.HasPrefix(rel, "..") {
		shown = filepath.Join("~", rel)
	}
	if os.Getenv("FERRY_DEMO") == "1" {
		shown = "~/Ferry"
	}
	srv := NewServer(&Server{store: store, thumbs: thumbs, host: host, code: code, id: id, appDir: cfg.DataDir, devices: devices,
		peers: peers, port: cfg.Port, folder: shown, web: web})
	srv.finder = newFinder(id, srv.computersChanged)

	plain := &http.Server{Handler: srv, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Port))
	if err != nil {
		return fmt.Errorf("port %d: %w (%v)", cfg.Port, ErrPortInUse, err)
	}
	go func() {
		if err := plain.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
			log.Printf("the page stopped: %v", err)
		}
	}()

	var secure *http.Server
	if cfg.TLSPort > 0 {
		secure, err = secureServer(srv, cfg.DataDir, host, cfg.TLSPort)
		if err != nil {
			log.Printf("the secure page is off: %v", err)
		} else {
			go func() {
				if err := secure.ListenAndServeTLS("", ""); !errors.Is(err, http.ErrServerClosed) {
					log.Printf("the secure page stopped: %v", err)
				}
			}()
		}
	}
	go srv.Watch()
	if cfg.Bonjour {
		stop := advertise(host, cfg.Port, srv.tlsPort, srv.id, srv.fingerprint(), srv.finder)
		defer stop()
	}
	// Other computers running Ferry: macOS lists them with dns-sd (elsewhere the announcer hears them).
	if runtime.GOOS == "darwin" && os.Getenv("FERRY_MDNS") != "builtin" {
		look, stopLooking := context.WithCancel(ctx)
		defer stopLooking()
		go srv.finder.browseDNSSD(look)
	}
	if cfg.Ready != nil {
		cfg.Ready(srv)
	}

	<-ctx.Done()
	plain.Close()
	if secure != nil {
		secure.Close()
	}
	return nil
}

// Running reports whether Ferry already answers on this computer's port (another copy of it).
func Running(port int) bool {
	c := http.Client{Timeout: 2 * time.Second}
	r, err := c.Get(fmt.Sprintf("http://localhost:%d/api/info", port))
	if err != nil {
		return false
	}
	defer r.Body.Close()
	return r.StatusCode == http.StatusOK && strings.HasPrefix(r.Header.Get("Content-Type"), "application/json")
}

// Folder is the shared folder, as shown ("~/Ferry").
func (s *Server) Folder() string { return s.folder }

// PhoneURLs are the links in the QR code: by .local name and by address.
func (s *Server) PhoneURLs() (byName, byIP string) { return s.phoneURLs() }

func secureServer(srv *Server, appDir string, host Host, port int) (*http.Server, error) {
	if host.LocalName == "" {
		return nil, errors.New("this computer has no local network name")
	}
	ca, err := LoadOrCreateAuthority(appDir, host.Name)
	if err != nil {
		return nil, err
	}
	srv.ca = ca
	cert, err := srv.issue(host)
	if err != nil {
		return nil, err
	}
	srv.cert.Store(&cert)
	srv.tlsPort, srv.caDER = port, ca.Cert.Raw
	return &http.Server{
		Addr: fmt.Sprintf(":%d", port), Handler: srv, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute,
		// A phone that hasn't trusted Ferry's certificate yet fails every handshake: expected, not news.
		ErrorLog: log.New(quietHandshakes{}, "", log.LstdFlags),
		TLSConfig: &tls.Config{
			MinVersion:     tls.VersionTLS12,
			GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return srv.cert.Load(), nil },
		},
	}, nil
}

// accessCode is the code other devices need, made once and kept so a phone stays paired.
// No look-alike characters (0/o, 1/l/i), so it can be typed from the screen.
func accessCode(appDir string) (string, error) {
	p := filepath.Join(appDir, "code")
	if b, err := os.ReadFile(p); err == nil && len(strings.TrimSpace(string(b))) == 8 {
		return strings.TrimSpace(string(b)), nil
	}
	code, err := makeCode()
	if err != nil {
		return "", err
	}
	return code, os.WriteFile(p, []byte(code), 0o600)
}

func makeCode() (string, error) {
	const alphabet = "abcdefghjkmnpqrstuvwxyz23456789"
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	code := make([]byte, 8)
	for i, b := range raw {
		code[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(code), nil
}

// quietHandshakes passes the secure server's errors to the log, except failed handshakes.
type quietHandshakes struct{}

func (quietHandshakes) Write(p []byte) (int, error) {
	if strings.Contains(string(p), "TLS handshake error") {
		return len(p), nil
	}
	return os.Stderr.Write(p)
}

// advertise announces Ferry on the network with Bonjour (_ferry._tcp), so the phone apps find this
// computer without an address: with macOS's dns-sd, elsewhere with Ferry's own responder. The
// apps tell computers apart by id, and recognise the one they paired with when its address changes.
func advertise(host Host, port, tlsPort int, id, fingerprint string, f *finder) (stop func()) {
	txt := []string{"v=1", "path=/", fmt.Sprintf("port=%d", port), "id=" + id}
	if tlsPort > 0 && fingerprint != "" {
		txt = append(txt, fmt.Sprintf("tls=%d", tlsPort), "fp="+fingerprint)
	}
	if runtime.GOOS != "darwin" || os.Getenv("FERRY_MDNS") == "builtin" {
		label := strings.Trim(strings.ToLower(strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
				return r
			}
			return '-'
		}, host.Name)), "-")
		return advertiseBuiltin(host, port, append(txt, "host="+label+"-ferry.local"), f)
	}
	if host.LocalName != "" {
		txt = append(txt, "host="+host.LocalName)
	}
	args := append([]string{"-R", host.Name, "_ferry._tcp", "local", fmt.Sprint(port)}, txt...)
	cmd := exec.Command("dns-sd", args...)
	if err := cmd.Start(); err != nil {
		log.Printf("couldn't announce Ferry on the network: %v", err)
		return func() {}
	}
	return func() { cmd.Process.Kill(); cmd.Wait() }
}
