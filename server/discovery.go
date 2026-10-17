package server

import (
	"bufio"
	"context"
	"net"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Nearby is another computer running Ferry on this network, as it announces itself.
type Nearby struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Host        string    `json:"host"` // an address (computers reach each other by address, which their certificates cover)
	Port        int       `json:"port"`
	TLSPort     int       `json:"tlsPort,omitempty"`
	Fingerprint string    `json:"-"`
	Seen        time.Time `json:"-"`
}

// finder keeps the list of other computers running Ferry here, found by Bonjour: with macOS's
// dns-sd, elsewhere by listening to the announcements Ferry's own responder hears.
type finder struct {
	self    string // this computer's id, left out
	changed func()

	mu    sync.Mutex
	found map[string]Nearby // by instance name
}

func newFinder(self string, changed func()) *finder {
	return &finder{self: self, changed: changed, found: map[string]Nearby{}}
}

func (f *finder) list() []Nearby {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Nearby
	for _, n := range f.found {
		if n.ID != "" && n.ID != f.self && time.Since(n.Seen) < 10*time.Minute {
			out = append(out, n)
		}
	}
	slices.SortFunc(out, func(a, b Nearby) int { return strings.Compare(a.Name, b.Name) })
	return out
}

func (f *finder) byID(id string) (Nearby, bool) {
	for _, n := range f.list() {
		if n.ID == id {
			return n, true
		}
	}
	return Nearby{}, false
}

func (f *finder) put(instance string, n Nearby) {
	f.mu.Lock()
	old, had := f.found[instance]
	n.Seen = time.Now()
	f.found[instance] = n
	f.mu.Unlock()
	if !had || old.Host != n.Host || old.ID != n.ID || old.Name != n.Name {
		f.changed()
	}
}

func (f *finder) drop(instance string) {
	f.mu.Lock()
	_, had := f.found[instance]
	delete(f.found, instance)
	f.mu.Unlock()
	if had {
		f.changed()
	}
}

// fromTXT fills in what a computer's text record says (id, ports, fingerprint).
func fromTXT(n *Nearby, txt []string) {
	for _, kv := range txt {
		k, v, _ := strings.Cut(kv, "=")
		switch k {
		case "id":
			n.ID = v
		case "port":
			if p, err := strconv.Atoi(v); err == nil && n.Port == 0 {
				n.Port = p
			}
		case "tls":
			n.TLSPort, _ = strconv.Atoi(v)
		case "fp":
			n.Fingerprint = v
		}
	}
}

// MARK: macOS (dns-sd)

var (
	browseLine  = regexp.MustCompile(`^\S+\s+(Add|Rmv)\s+\d+\s+\d+\s+\S+\s+_ferry\._tcp\.\s+(.+)$`)
	reachedLine = regexp.MustCompile(`can be reached at (\S+?)\.?:(\d+)`)
)

// browseDNSSD follows `dns-sd -B` and looks each computer up with `dns-sd -L` until ctx ends.
func (f *finder) browseDNSSD(ctx context.Context) {
	for ctx.Err() == nil {
		cmd := exec.CommandContext(ctx, "dns-sd", "-B", "_ferry._tcp", "local")
		out, err := cmd.StdoutPipe()
		if err != nil || cmd.Start() != nil {
			return
		}
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			m := browseLine.FindStringSubmatch(strings.TrimRight(sc.Text(), " "))
			if m == nil {
				continue
			}
			instance := m[2]
			if m[1] == "Rmv" {
				f.drop(instance)
				continue
			}
			go f.lookUp(ctx, instance)
		}
		cmd.Wait()
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
		}
	}
}

func (f *finder) lookUp(ctx context.Context, instance string) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "dns-sd", "-L", instance, "_ferry._tcp", "local")
	out, err := cmd.StdoutPipe()
	if err != nil || cmd.Start() != nil {
		return
	}
	defer cmd.Wait()
	sc := bufio.NewScanner(out)
	n := Nearby{Name: instance}
	host := ""
	for sc.Scan() {
		line := sc.Text()
		if m := reachedLine.FindStringSubmatch(line); m != nil {
			host = m[1]
			n.Port, _ = strconv.Atoi(m[2])
			continue
		}
		if host != "" && strings.HasPrefix(line, " ") {
			fromTXT(&n, strings.Fields(strings.ReplaceAll(line, `\ `, " ")))
			n.Host = addressOf(ctx, host)
			if n.Host != "" && n.ID != "" {
				f.put(instance, n)
			}
			cancel()
			return
		}
	}
}

// addressOf is a private IPv4 address for a .local name.
func addressOf(ctx context.Context, host string) string {
	if ip := net.ParseIP(host); ip != nil {
		return ip.String()
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", host)
	if err != nil {
		return ""
	}
	// The network address first; this computer's own loopback only if that's all there is.
	for _, ip := range ips {
		if ip.IsPrivate() {
			return ip.String()
		}
	}
	for _, ip := range ips {
		if ip.IsLoopback() {
			return ip.String()
		}
	}
	return ""
}
