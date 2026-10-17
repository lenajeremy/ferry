package server

import (
	"context"
	"encoding/binary"
	"log"
	"net"
	"slices"
	"strings"
	"sync"
	"time"
)

// A minimal multicast DNS responder (RFC 6762/6763) for _ferry._tcp, used where macOS's dns-sd isn't
// there (Windows, Linux): it answers the phone apps' "who runs Ferry?" with this computer's address.
type mdnsResponder struct {
	instance string // "Alex's PC"
	host     string // a host name of its own: "alexs-pc-ferry.local."
	port     uint16
	txt      []string
	ips      func() []net.IP // current IPv4 addresses
	mu       sync.Mutex      // one send at a time (each picks its interface)

	// Other computers' announcements it hears, put together into what finder lists.
	finder   *finder
	heardMu  sync.Mutex
	heardSRV map[string]srvRecord // by instance
	heardTXT map[string][]string  // by instance
	heardA   map[string]net.IP    // by host name
}

type srvRecord struct {
	host string
	port int
}

const (
	mdnsService = "_ferry._tcp.local."
	dnsA        = 1
	dnsPTR      = 12
	dnsTXT      = 16
	dnsSRV      = 33
	dnsANY      = 255
)

var mdnsGroup = &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}

func (m *mdnsResponder) instanceName() string { return m.instance + "." + mdnsService }

func (m *mdnsResponder) run(ctx context.Context) error {
	conn, err := net.ListenMulticastUDP("udp4", nil, mdnsGroup)
	if err != nil {
		return err
	}
	go func() { <-ctx.Done(); conn.Close() }()
	// Announce on start (three times, a second apart), then answer questions.
	go func() {
		for i := 0; i < 3 && ctx.Err() == nil; i++ {
			m.send(conn, m.answer(true, true, true), mdnsGroup)
			time.Sleep(time.Second)
		}
	}()
	// Ask who else runs Ferry here now and then (their answers go to everyone, us included).
	if m.finder != nil {
		go func() {
			for ctx.Err() == nil {
				m.send(conn, query(mdnsService, dnsPTR), mdnsGroup)
				select {
				case <-ctx.Done():
				case <-time.After(60 * time.Second):
				}
			}
		}()
	}
	buf := make([]byte, 9000)
	for {
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			continue
		}
		if n >= 12 && buf[2]&0x80 != 0 {
			m.heard(buf[:n])
			continue
		}
		ptr, srv, a, unicast := m.wants(buf[:n])
		if !ptr && !srv && !a {
			continue
		}
		reply := m.answer(ptr, srv, a)
		if unicast {
			m.send(conn, reply, from)
		} else {
			m.send(conn, reply, mdnsGroup)
		}
	}
}

// send replies to one asker directly, or to the group on every network this computer is on: from
// port 5353 (others ignore mDNS answers from anywhere else), naming each interface, since the default
// route may be a VPN that carries no multicast.
func (m *mdnsResponder) send(conn *net.UDPConn, msg []byte, to *net.UDPAddr) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !to.IP.Equal(mdnsGroup.IP) {
		conn.WriteToUDP(msg, to)
		return
	}
	for _, ip := range m.ips() {
		if setMulticastInterface(conn, ip) == nil {
			conn.WriteToUDP(msg, mdnsGroup)
		}
	}
}

// wants reads a query and says which of our records it asks for.
func (m *mdnsResponder) wants(msg []byte) (ptr, srv, a, unicast bool) {
	if len(msg) < 12 || msg[2]&0x80 != 0 { // too short, or a response
		return
	}
	qd := int(binary.BigEndian.Uint16(msg[4:6]))
	off := 12
	for i := 0; i < qd; i++ {
		name, next, ok := readName(msg, off)
		if !ok || next+4 > len(msg) {
			return
		}
		qtype := binary.BigEndian.Uint16(msg[next : next+2])
		qclass := binary.BigEndian.Uint16(msg[next+2 : next+4])
		off = next + 4
		name = strings.ToLower(name)
		if qclass&0x8000 != 0 {
			unicast = true
		}
		switch {
		case name == mdnsService && (qtype == dnsPTR || qtype == dnsANY):
			ptr = true
		case name == "_services._dns-sd._udp.local." && qtype == dnsPTR:
			ptr = true
		case name == strings.ToLower(m.instanceName()) && (qtype == dnsSRV || qtype == dnsTXT || qtype == dnsANY):
			srv = true
		case name == strings.ToLower(m.host) && (qtype == dnsA || qtype == dnsANY):
			a = true
		}
	}
	return
}

// readName reads a (possibly compressed) domain name, returning it with a trailing dot.
func readName(msg []byte, off int) (string, int, bool) {
	var labels []string
	next := -1
	for hops := 0; hops < 20; hops++ {
		if off >= len(msg) {
			return "", 0, false
		}
		l := int(msg[off])
		switch {
		case l == 0:
			if next < 0 {
				next = off + 1
			}
			return strings.Join(labels, ".") + ".", next, true
		case l&0xC0 == 0xC0:
			if off+1 >= len(msg) {
				return "", 0, false
			}
			if next < 0 {
				next = off + 2
			}
			off = int(binary.BigEndian.Uint16(msg[off:off+2]) & 0x3FFF)
		default:
			if off+1+l > len(msg) {
				return "", 0, false
			}
			labels = append(labels, string(msg[off+1:off+1+l]))
			off += 1 + l
		}
	}
	return "", 0, false
}

// answer builds a response: the pointer to our instance, its service and text records, and the
// host's addresses (always included, so one packet is enough).
func (m *mdnsResponder) answer(ptr, srv, a bool) []byte {
	var records [][]byte
	inst := m.instanceName()
	if ptr {
		records = append(records, record(mdnsService, dnsPTR, false, 4500, encodeName(inst)))
	}
	if ptr || srv {
		var s []byte
		s = binary.BigEndian.AppendUint16(s, 0) // priority
		s = binary.BigEndian.AppendUint16(s, 0) // weight
		s = binary.BigEndian.AppendUint16(s, m.port)
		s = append(s, encodeName(m.host)...)
		records = append(records, record(inst, dnsSRV, true, 120, s))
		var t []byte
		for _, kv := range m.txt {
			t = append(t, byte(len(kv)))
			t = append(t, kv...)
		}
		records = append(records, record(inst, dnsTXT, true, 4500, t))
	}
	for _, ip := range m.ips() {
		records = append(records, record(m.host, dnsA, true, 120, ip.To4()))
	}
	msg := []byte{0, 0, 0x84, 0, 0, 0}                             // id 0, response + authoritative, no questions
	msg = binary.BigEndian.AppendUint16(msg, uint16(len(records))) // answers
	msg = append(msg, 0, 0, 0, 0)                                  // no authority or additional records
	for _, r := range records {
		msg = append(msg, r...)
	}
	return msg
}

func record(name string, typ uint16, flush bool, ttl uint32, data []byte) []byte {
	r := encodeName(name)
	r = binary.BigEndian.AppendUint16(r, typ)
	class := uint16(1)
	if flush {
		class |= 0x8000
	}
	r = binary.BigEndian.AppendUint16(r, class)
	r = binary.BigEndian.AppendUint32(r, ttl)
	r = binary.BigEndian.AppendUint16(r, uint16(len(data)))
	return append(r, data...)
}

// encodeName writes "Alex's PC._ferry._tcp.local." as labels. The instance label may hold dots
// and spaces, so the service suffix is split off first.
func encodeName(name string) []byte {
	var labels []string
	if strings.HasSuffix(name, "."+mdnsService) {
		labels = append([]string{strings.TrimSuffix(name, "."+mdnsService)}, strings.Split(strings.TrimSuffix(mdnsService, "."), ".")...)
	} else {
		labels = strings.Split(strings.TrimSuffix(name, "."), ".")
	}
	var out []byte
	for _, l := range labels {
		if len(l) > 63 {
			l = l[:63]
		}
		out = append(out, byte(len(l)))
		out = append(out, l...)
	}
	return append(out, 0)
}

// advertiseBuiltin runs the responder until stop is called, telling f about other computers it hears.
func advertiseBuiltin(host Host, port int, txt []string, f *finder) (stop func()) {
	label := strings.ToLower(strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, host.Name))
	m := &mdnsResponder{instance: host.Name, host: strings.Trim(label, "-") + "-ferry.local.", port: uint16(port), txt: txt, ips: lanIPs, finder: f}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		if err := m.run(ctx); err != nil {
			log.Printf("couldn't announce Ferry on the network (%v); the apps can still connect with the QR code", err)
		}
	}()
	return cancel
}

// query is a one-question mDNS query.
func query(name string, typ uint16) []byte {
	msg := []byte{0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0}
	msg = append(msg, encodeName(name)...)
	msg = binary.BigEndian.AppendUint16(msg, typ)
	return binary.BigEndian.AppendUint16(msg, 1)
}

// heard reads another responder's answer and tells the finder about any computer running Ferry in it.
func (m *mdnsResponder) heard(msg []byte) {
	if m.finder == nil {
		return
	}
	counts := []int{int(binary.BigEndian.Uint16(msg[4:6])), int(binary.BigEndian.Uint16(msg[6:8])),
		int(binary.BigEndian.Uint16(msg[8:10])), int(binary.BigEndian.Uint16(msg[10:12]))}
	off := 12
	for i := 0; i < counts[0]; i++ { // questions
		_, next, ok := readName(msg, off)
		if !ok || next+4 > len(msg) {
			return
		}
		off = next + 4
	}
	m.heardMu.Lock()
	if m.heardSRV == nil {
		m.heardSRV, m.heardTXT, m.heardA = map[string]srvRecord{}, map[string][]string{}, map[string]net.IP{}
	}
	var instances []string
	var gone []string
	for i := 0; i < counts[1]+counts[2]+counts[3]; i++ {
		name, next, ok := readName(msg, off)
		if !ok || next+10 > len(msg) {
			break
		}
		typ := binary.BigEndian.Uint16(msg[next : next+2])
		ttl := binary.BigEndian.Uint32(msg[next+4 : next+8])
		size := int(binary.BigEndian.Uint16(msg[next+8 : next+10]))
		data := next + 10
		if data+size > len(msg) {
			break
		}
		off = data + size
		lower := strings.ToLower(name)
		switch typ {
		case dnsPTR:
			if lower != mdnsService {
				continue
			}
			if target, _, ok := readName(msg, data); ok {
				instances = append(instances, target)
				if ttl == 0 {
					gone = append(gone, target)
				}
			}
		case dnsSRV:
			if size < 7 || !strings.HasSuffix(lower, "."+mdnsService) {
				continue
			}
			if target, _, ok := readName(msg, data+6); ok {
				m.heardSRV[name] = srvRecord{host: strings.ToLower(target), port: int(binary.BigEndian.Uint16(msg[data+4 : data+6]))}
				instances = append(instances, name)
			}
		case dnsTXT:
			if !strings.HasSuffix(lower, "."+mdnsService) {
				continue
			}
			var kv []string
			for p := data; p < data+size; {
				l := int(msg[p])
				if p+1+l > data+size {
					break
				}
				kv = append(kv, string(msg[p+1:p+1+l]))
				p += 1 + l
			}
			m.heardTXT[name] = kv
			instances = append(instances, name)
		case dnsA:
			if size == 4 {
				m.heardA[lower] = net.IP(slices.Clone(msg[data : data+4]))
			}
		}
	}
	type ready struct {
		instance string
		n        Nearby
	}
	var found []ready
	for _, inst := range instances {
		srv, ok := m.heardSRV[inst]
		ip := m.heardA[srv.host]
		if !ok || ip == nil || m.heardTXT[inst] == nil {
			continue
		}
		n := Nearby{Name: strings.TrimSuffix(inst, "."+mdnsService), Host: ip.String(), Port: srv.port}
		fromTXT(&n, m.heardTXT[inst])
		found = append(found, ready{inst, n})
	}
	m.heardMu.Unlock()
	for _, inst := range gone {
		m.finder.drop(strings.TrimSuffix(inst, "."+mdnsService))
	}
	for _, r := range found {
		if !slices.Contains(gone, r.instance) {
			m.finder.put(r.n.Name, r.n)
		}
	}
}
