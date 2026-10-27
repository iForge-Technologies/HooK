package probe

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"
)

var DNSRecords = []string{"A", "AAAA", "CNAME", "MX", "TXT", "NS"}

func checkDNS(ctx context.Context, s Spec) Result {
	c := s
	resolver := net.DefaultResolver
	if c.DNSResolver != "" {
		addr := c.DNSResolver
		if _, _, err := net.SplitHostPort(addr); err != nil {
			addr = net.JoinHostPort(addr, "53")
		}
		resolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return s.dialer().DialContext(ctx, network, addr)
		}}
	}

	host := strings.TrimSpace(s.Target)
	start := time.Now()
	var records []string
	var err error
	switch strings.ToUpper(c.DNSRecord) {
	case "", "A", "AAAA":
		network := "ip4"
		if strings.EqualFold(c.DNSRecord, "AAAA") {
			network = "ip6"
		}
		var ips []net.IP
		if ips, err = resolver.LookupIP(ctx, network, host); err == nil {
			for _, ip := range ips {
				records = append(records, ip.String())
			}
		}
	case "CNAME":
		var cname string
		if cname, err = resolver.LookupCNAME(ctx, host); err == nil {
			records = append(records, cname)
		}
	case "MX":
		var mx []*net.MX
		if mx, err = resolver.LookupMX(ctx, host); err == nil {
			for _, r := range mx {
				records = append(records, fmt.Sprintf("%d %s", r.Pref, r.Host))
			}
		}
	case "TXT":
		records, err = resolver.LookupTXT(ctx, host)
	case "NS":
		var ns []*net.NS
		if ns, err = resolver.LookupNS(ctx, host); err == nil {
			for _, r := range ns {
				records = append(records, r.Host)
			}
		}
	default:
		return Result{Msg: "unsupported record type " + c.DNSRecord}
	}
	lat := time.Since(start)
	if err != nil {
		return Result{Latency: lat, Msg: ErrMsg(err)}
	}
	if len(records) == 0 {
		return Result{Latency: lat, Msg: "No records"}
	}
	if s.publicOnly {
		for _, r := range records {
			if ip, err := netip.ParseAddr(r); err == nil && !isPublic(ip) {
				return Result{Latency: lat, Msg: Refused + "answer is not a public address"}
			}
		}
	}
	got := strings.Join(records, ", ")
	if want := strings.TrimSpace(c.DNSExpected); want != "" && !dnsMatch(records, want, strings.ToUpper(c.DNSRecord)) {
		return Result{Latency: lat, Msg: fmt.Sprintf("Expected %s, got %s", want, got)}
	}
	return Result{OK: true, Latency: lat, Msg: got}
}

// dnsMatch accepts a record that equals the expected value, ignoring case and a
// trailing dot. TXT records only need to contain it; MX also matches the host alone.
func dnsMatch(records []string, want, kind string) bool {
	norm := func(s string) string { return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), ".") }
	w := norm(want)
	for _, r := range records {
		n := norm(r)
		switch {
		case n == w:
			return true
		case kind == "TXT" && strings.Contains(n, w):
			return true
		case kind == "MX" && strings.HasSuffix(n, " "+w):
			return true
		}
	}
	return false
}
