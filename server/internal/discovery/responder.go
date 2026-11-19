package discovery

import (
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"
	"golang.org/x/net/ipv4"
)

// responder is a deliberately small mDNS/DNS-SD responder for exactly one
// FlowDrop service. It exists instead of zeroconf's RegisterProxy because the
// upstream responder answers queries that arrive on *any* interface of a
// multi-homed host; this responder drops every packet whose control-message
// interface index is not the bound LAN interface. It still uses miekg/dns for
// wire encoding and zeroconf for the best-effort conflict scan.
type responder struct {
	pc           *ipv4.PacketConn
	conn         *net.UDPConn
	iface        net.Interface
	ip           net.IP
	port         int
	instanceName string
	hostName     string
	serviceName  string
	txt          []string
	ttl          uint32
	log          *slog.Logger

	ptr   dns.RR
	srv   dns.RR
	txtRR dns.RR
	a     dns.RR

	shutdown     chan struct{}
	announceDone chan struct{}
	once         sync.Once
	wg           sync.WaitGroup
	writeMu      sync.Mutex
}

func startResponder(instance string, t Target, host string, txt []string, iface net.Interface, log *slog.Logger) (Publisher, error) {
	if log == nil {
		log = slog.Default()
	}
	group := &net.UDPAddr{IP: net.ParseIP("224.0.0.251"), Port: 5353}
	conn, err := net.ListenMulticastUDP("udp4", &iface, group)
	if err != nil {
		return nil, err
	}
	if err := conn.SetReadBuffer(1 << 20); err != nil {
		log.Debug("mDNS read buffer", "err", err)
	}
	pc := ipv4.NewPacketConn(conn)
	if err := pc.SetControlMessage(ipv4.FlagInterface, true); err != nil {
		conn.Close()
		return nil, fmt.Errorf("enable mDNS interface control messages: %w", err)
	}
	if err := pc.SetMulticastInterface(&iface); err != nil {
		conn.Close()
		return nil, fmt.Errorf("select mDNS multicast interface: %w", err)
	}
	_ = pc.SetMulticastLoopback(true)
	_ = pc.SetMulticastTTL(255)

	r := &responder{
		pc:           pc,
		conn:         conn,
		iface:        iface,
		ip:           t.IP.To4(),
		port:         t.Port,
		instanceName: dns.Fqdn(instance + "." + ServiceType + "." + Domain),
		hostName:     dns.Fqdn(host),
		serviceName:  ServiceTypeFQDN,
		txt:          append([]string(nil), txt...),
		ttl:          120,
		log:          log,
		shutdown:     make(chan struct{}),
		announceDone: make(chan struct{}),
	}
	r.buildRecords()

	r.wg.Add(2)
	go r.readLoop()
	go r.announceLoop()
	return r, nil
}

// Shutdown sends a TTL=0 goodbye on the bound interface, closes the UDP 5353
// socket and waits for the responder goroutines. Safe to call twice.
func (r *responder) Shutdown() {
	r.once.Do(func() {
		close(r.shutdown)
		// Wait for the announcer to stop before sending the goodbye, otherwise
		// a scheduled announcement could recreate the record after TTL=0.
		<-r.announceDone
		r.sendAll(0)
		r.conn.Close()
		r.wg.Wait()
	})
}

func (r *responder) buildRecords() {
	ttl := r.ttl
	uniqueClass := uint16(dns.ClassINET) | 0x8000
	r.ptr = &dns.PTR{
		Hdr: dns.RR_Header{Name: r.serviceName, Rrtype: dns.TypePTR, Class: dns.ClassINET, Ttl: ttl},
		Ptr: r.instanceName,
	}
	r.srv = &dns.SRV{
		Hdr:      dns.RR_Header{Name: r.instanceName, Rrtype: dns.TypeSRV, Class: uniqueClass, Ttl: ttl},
		Port:     uint16(r.port),
		Target:   r.hostName,
		Priority: 0,
		Weight:   0,
	}
	r.txtRR = &dns.TXT{
		Hdr: dns.RR_Header{Name: r.instanceName, Rrtype: dns.TypeTXT, Class: uniqueClass, Ttl: ttl},
		Txt: append([]string(nil), r.txt...),
	}
	r.a = &dns.A{
		Hdr: dns.RR_Header{Name: r.hostName, Rrtype: dns.TypeA, Class: uniqueClass, Ttl: ttl},
		A:   append(net.IP(nil), r.ip...),
	}
}

func (r *responder) readLoop() {
	defer r.wg.Done()
	buf := make([]byte, 9000)
	for {
		n, cm, from, err := r.pc.ReadFrom(buf)
		if err != nil {
			select {
			case <-r.shutdown:
				return
			default:
				continue
			}
		}
		// The core of the interface-scoping guarantee: ignore anything that
		// arrived on another NIC (tailscale0, docker0, ...).
		if cm == nil || cm.IfIndex != r.iface.Index {
			continue
		}
		var msg dns.Msg
		if err := msg.Unpack(buf[:n]); err != nil {
			continue
		}
		r.handleQuery(&msg, from)
	}
}

func (r *responder) announceLoop() {
	defer r.wg.Done()
	defer close(r.announceDone)
	// Two unsolicited announcements one second apart, plus one later retry.
	for i := 0; i < 3; i++ {
		if i > 0 {
			select {
			case <-time.After(time.Second):
			case <-r.shutdown:
				return
			}
		}
		r.sendAll(r.ttl)
	}
}

func (r *responder) handleQuery(q *dns.Msg, from net.Addr) {
	select {
	case <-r.shutdown:
		return
	default:
	}
	if q.Response {
		return
	}
	for _, question := range q.Question {
		name := dns.Fqdn(question.Name)
		unicast := question.Qclass&0x8000 != 0
		var answer, extra []dns.RR
		switch {
		case equalName(name, r.serviceName):
			if question.Qtype == dns.TypePTR || question.Qtype == dns.TypeANY {
				answer = append(answer, r.ptr)
				extra = append(extra, r.srv, r.txtRR, r.a)
			}
		case equalName(name, r.instanceName):
			if question.Qtype == dns.TypeSRV || question.Qtype == dns.TypeANY {
				answer = append(answer, r.srv)
				extra = append(extra, r.txtRR, r.a)
			}
			if question.Qtype == dns.TypeTXT || question.Qtype == dns.TypeANY {
				answer = append(answer, r.txtRR)
				extra = append(extra, r.a)
			}
		case equalName(name, r.hostName):
			if question.Qtype == dns.TypeA || question.Qtype == dns.TypeANY {
				answer = append(answer, r.a)
			}
		case equalName(name, "_services._dns-sd._udp.local."):
			if question.Qtype == dns.TypePTR || question.Qtype == dns.TypeANY {
				answer = append(answer, &dns.PTR{
					Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypePTR, Class: dns.ClassINET, Ttl: r.ttl},
					Ptr: r.serviceName,
				})
			}
		}
		if len(answer) == 0 {
			continue
		}
		resp := new(dns.Msg)
		resp.SetReply(q)
		resp.Authoritative = true
		resp.RecursionDesired = false
		resp.Question = nil // RFC 6762 section 6
		if !unicast {
			resp.Id = 0 // RFC 6762: multicast responses use ID 0
		}
		resp.Answer = answer
		resp.Extra = extra
		r.writeMsg(resp, unicast, from)
	}
}

func (r *responder) sendAll(ttl uint32) {
	ptr := dns.Copy(r.ptr)
	srv := dns.Copy(r.srv)
	txt := dns.Copy(r.txtRR)
	a := dns.Copy(r.a)
	for _, rr := range []dns.RR{ptr, srv, txt, a} {
		rr.Header().Ttl = ttl
	}
	msg := new(dns.Msg)
	msg.Response = true
	msg.Authoritative = true
	msg.Id = 0
	msg.Answer = []dns.RR{ptr}
	msg.Extra = []dns.RR{srv, txt, a}
	r.writeMsg(msg, false, nil)
}

func (r *responder) writeMsg(msg *dns.Msg, unicast bool, from net.Addr) {
	msg.Compress = true
	buf, err := msg.Pack()
	if err != nil {
		r.log.Debug("mDNS pack failed", "err", err)
		return
	}
	cm := &ipv4.ControlMessage{IfIndex: r.iface.Index}
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	if unicast && from != nil {
		_, err = r.pc.WriteTo(buf, cm, from)
	} else {
		_, err = r.pc.WriteTo(buf, cm, &net.UDPAddr{IP: net.ParseIP("224.0.0.251"), Port: 5353})
	}
	if err != nil {
		select {
		case <-r.shutdown:
		default:
			r.log.Debug("mDNS write failed", "err", err)
		}
	}
}

func equalName(a, b string) bool {
	return strings.EqualFold(dns.Fqdn(a), dns.Fqdn(b))
}
