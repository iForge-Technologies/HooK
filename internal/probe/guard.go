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
func RunPublicOnly(ctx context.Context, s Spec) Result {
	s.publicOnly = true
	if s.Type == "dns" && s.DNSResolver != "" {
		host := s.DNSResolver
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if ip, err := netip.ParseAddr(host); err == nil && !isPublic(ip) {
			return Result{Msg: Refused + "resolver is a private address"}
		}
	}
	return Run(ctx, s)
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
