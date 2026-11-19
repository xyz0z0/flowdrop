// Command mdnsprobe is a small, independent mDNS/DNS-SD protocol probe used to
// verify the Hub's discovery announcement without going through the Hub's
// zeroconf registration code or a normal HTTP health check.
//
// It joins 224.0.0.251:5353 on one interface, sends a PTR query for the
// FlowDrop service type and prints every matching PTR/SRV/TXT/A record. Exit
// status is 1 when no record was received, which is the expected result when
// discovery is disabled or the bind address is ineligible.
//
// Example:
//
//	cd server && go run ./cmd/mdnsprobe -iface eno1 -timeout 4s
package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/miekg/dns"
	"golang.org/x/net/ipv4"
)

const defaultService = "_flowdrop._tcp.local."

func main() {
	ifaceName := flag.String("iface", "", "interface to join and send on (required, e.g. eno1)")
	service := flag.String("service", defaultService, "DNS-SD service type to browse")
	timeout := flag.Duration("timeout", 4*time.Second, "how long to listen")
	flag.Parse()
	if *ifaceName == "" {
		fmt.Fprintln(os.Stderr, "usage: mdnsprobe -iface <name> [-service "+defaultService+"] [-timeout 4s]")
		os.Exit(2)
	}

	iface, err := net.InterfaceByName(*ifaceName)
	if err != nil {
		fmt.Fprintln(os.Stderr, "interface:", err)
		os.Exit(2)
	}
	group := &net.UDPAddr{IP: net.ParseIP("224.0.0.251"), Port: 5353}
	conn, err := net.ListenMulticastUDP("udp4", iface, group)
	if err != nil {
		fmt.Fprintln(os.Stderr, "listen multicast:", err)
		os.Exit(2)
	}
	defer conn.Close()
	_ = conn.SetReadBuffer(1 << 20)
	if err := conn.SetDeadline(time.Now().Add(*timeout)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	q := new(dns.Msg)
	q.Id = 0
	q.RecursionDesired = false
	q.Question = []dns.Question{{Name: *service, Qtype: dns.TypePTR, Qclass: dns.ClassINET}}
	buf, err := q.Pack()
	if err != nil {
		fmt.Fprintln(os.Stderr, "pack:", err)
		os.Exit(2)
	}

	// Send from a separate ephemeral socket with the outgoing multicast
	// interface pinned, so the query leaves the intended NIC.
	send, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		fmt.Fprintln(os.Stderr, "send socket:", err)
		os.Exit(2)
	}
	defer send.Close()
	pc := ipv4.NewPacketConn(send)
	_ = pc.SetMulticastInterface(iface)
	_ = pc.SetMulticastLoopback(true)
	cm := &ipv4.ControlMessage{IfIndex: iface.Index}
	for i := 0; i < 2; i++ {
		if _, err := pc.WriteTo(buf, cm, group); err != nil {
			fmt.Fprintln(os.Stderr, "send:", err)
			os.Exit(2)
		}
		time.Sleep(100 * time.Millisecond)
	}

	found := false
	seen := make(map[string]struct{})
	for {
		b := make([]byte, 9000)
		n, from, err := conn.ReadFromUDP(b)
		if err != nil {
			break
		}
		var msg dns.Msg
		if err := msg.Unpack(b[:n]); err != nil {
			continue
		}
		records := append(append(append([]dns.RR{}, msg.Answer...), msg.Ns...), msg.Extra...)
		for _, rr := range records {
			line := recordLine(rr, *service)
			if line == "" {
				continue
			}
			if _, ok := seen[line]; ok {
				continue
			}
			seen[line] = struct{}{}
			found = true
			fmt.Printf("from=%s %s\n", from, line)
		}
	}
	if !found {
		fmt.Printf("NO_RECORDS service=%s\n", *service)
		os.Exit(1)
	}
}

func recordLine(rr dns.RR, service string) string {
	lowerService := strings.ToLower(service)
	switch v := rr.(type) {
	case *dns.PTR:
		if strings.EqualFold(v.Hdr.Name, service) {
			return fmt.Sprintf("PTR %s -> %s ttl=%d", v.Hdr.Name, v.Ptr, v.Hdr.Ttl)
		}
	case *dns.SRV:
		if strings.Contains(strings.ToLower(v.Hdr.Name), lowerService) {
			return fmt.Sprintf("SRV %s port=%d target=%s ttl=%d", v.Hdr.Name, v.Port, v.Target, v.Hdr.Ttl)
		}
	case *dns.TXT:
		if strings.Contains(strings.ToLower(v.Hdr.Name), lowerService) {
			return fmt.Sprintf("TXT %s %q ttl=%d", v.Hdr.Name, v.Txt, v.Hdr.Ttl)
		}
	case *dns.A:
		if strings.Contains(strings.ToLower(v.Hdr.Name), "flowdrop-") {
			return fmt.Sprintf("A %s %s ttl=%d", v.Hdr.Name, v.A, v.Hdr.Ttl)
		}
	}
	return ""
}
