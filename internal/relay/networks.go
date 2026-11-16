package relay

import (
	"fmt"
	"net/netip"
	"strings"
)

// ParseNetworks parses "10.0.0.0/8", "192.168.1.0/24", "fd00::/8" or single
// addresses ("10.1.2.3", taken as /32 or /128) for Config.OpenNetworks.
func ParseNetworks(list []string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, item := range list {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if strings.Contains(item, "/") {
			p, err := netip.ParsePrefix(item)
			if err != nil {
				return nil, fmt.Errorf("invalid network %q: %w", item, err)
			}
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(item)
		if err != nil {
			return nil, fmt.Errorf("invalid network or address %q", item)
		}
		a = a.Unmap()
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, nil
}

// inOpenNetworks reports whether a client address belongs to a network that
// may create shares without a token.
func (s *Server) inOpenNetworks(ip string) bool {
	if len(s.cfg.OpenNetworks) == 0 {
		return false
	}
	a, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return false
	}
	a = a.Unmap().WithZone("")
	for _, p := range s.cfg.OpenNetworks {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// tokenRequired reports whether a client at ip must present a token to
// create shares.
func (s *Server) tokenRequired(ip string) bool {
	return len(s.cfg.Tokens) > 0 && !s.inOpenNetworks(ip)
}
