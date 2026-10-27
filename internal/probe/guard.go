package probe

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"syscall"
)

// RunPublicOnly runs a check that may only connect to public IP addresses.
// Agents use it for probes requested by the hub, so a compromised hub can't
// turn them into scanners of their own private networks (or cloud metadata
// endpoints). The check happens at connect time, so redirects and DNS tricks
// can't get around it.
//
// Name resolution is part of the guarded surface too: a DNS check must name a
// public resolver by IP (the server's own resolver may know private names),
// answers with private addresses are withheld, and every refusal carries a
// fixed message, so nothing about the private network reaches the hub.
func RunPublicOnly(ctx context.Context, s Spec) Result {
	s.publicOnly = true
	if s.Type == "dns" {
		host := s.DNSResolver
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if ip, err := netip.ParseAddr(host); err != nil || !isPublic(ip) {
			return Result{Msg: Refused + "DNS checks from other locations need a public resolver IP"}
		}
	}
	r := Run(ctx, s)
	if WasRefused(r) {
		return Result{Latency: r.Latency, Msg: Refused + "target is not a public address"}
	}
	return r
}

// Refused marks results where the agent declined to probe; they say nothing
// about whether the target is up.
const Refused = "refused: "

var errPrivate = errors.New(Refused + "target resolves to a private address")

// WasRefused reports whether a result is a refusal rather than a real check.
func WasRefused(r Result) bool { return strings.Contains(r.Msg, Refused) }

func isPublic(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsValid() && !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() &&
		!ip.IsLinkLocalMulticast() && !ip.IsMulticast() && !ip.IsUnspecified() && !ip.IsInterfaceLocalMulticast() &&
		!netip.MustParsePrefix("100.64.0.0/10").Contains(ip) // carrier-grade NAT
}

// targetIsPublic is publicTarget; tests swap it to pin loopback servers.
var targetIsPublic = publicTarget

// publicTarget reports whether a check's configured host resolves only to
// public addresses.
func publicTarget(ctx context.Context, host string) bool {
	if ip, err := netip.ParseAddr(host); err == nil {
		return isPublic(ip)
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return false
	}
	for _, ip := range ips {
		if !isPublic(ip) {
			return false
		}
	}
	return true
}

// dialer returns a net.Dialer that, for public-only specs, refuses to
// connect anywhere that isn't a public address.
func (s Spec) dialer() *net.Dialer {
	d := &net.Dialer{}
	if s.publicOnly {
		d.Control = func(_, address string, _ syscall.RawConn) error {
			ap, err := netip.ParseAddrPort(address)
			if err != nil {
				return fmt.Errorf("refused: %w", err)
			}
			if !isPublic(ap.Addr()) {
				return errPrivate
			}
			return nil
		}
	}
	return d
}
