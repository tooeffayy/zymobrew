package server

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// realIPCtxKey carries the resolved client IP through the request context.
// Handlers should call clientIPFromContext rather than parsing r.RemoteAddr
// themselves — the middleware has already done the work and parsing twice
// invites disagreement.
type realIPCtxKey struct{}

// realIP returns a middleware that resolves the real client IP from
// X-Forwarded-For when the immediate connection peer is in the trusted
// proxy set. Otherwise the raw r.RemoteAddr wins.
//
// XFF is walked right-to-left. We pop entries that are themselves trusted
// proxies (i.e. proxy-to-proxy hops we control) until we hit one that
// isn't — that's the real client. If we run out of XFF entries while still
// in trusted territory, we fall back to the connection peer.
//
// Trusting only the leftmost or only the rightmost XFF entry is incorrect
// under multi-hop, which is why we walk the chain.
//
// Behavior with `trusted == nil` (TRUSTED_PROXIES unset): XFF is ignored
// entirely. The connection peer is the client. This is the safe default —
// don't widen it to "trust loopback" or similar; an operator who actually
// wants loopback trust can set TRUSTED_PROXIES=127.0.0.1,::1.
func realIP(trusted []netip.Prefix) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			peer, ok := parseHostPort(r.RemoteAddr)
			if !ok {
				next.ServeHTTP(w, r)
				return
			}
			resolved := peer
			if len(trusted) > 0 && isTrusted(peer, trusted) {
				resolved = walkXFF(r.Header.Values("X-Forwarded-For"), peer, trusted)
			}
			ctx := context.WithValue(r.Context(), realIPCtxKey{}, resolved)
			// Rewrite r.RemoteAddr too so existing helpers (clientIP, logging,
			// future middleware) see the resolved value without code changes.
			rebuilt := joinHostPort(resolved, r.RemoteAddr)
			r2 := r.WithContext(ctx)
			r2.RemoteAddr = rebuilt
			next.ServeHTTP(w, r2)
		})
	}
}

// clientIPFromContext returns the resolved client IP that the realIP
// middleware stashed on the context. If the middleware wasn't applied
// (e.g. tests that bypass the chain), returns the zero value.
func clientIPFromContext(ctx context.Context) netip.Addr {
	a, _ := ctx.Value(realIPCtxKey{}).(netip.Addr)
	return a
}

// walkXFF walks an X-Forwarded-For chain right-to-left, popping entries
// that are themselves in the trusted set. Returns the first untrusted
// entry encountered, or `peer` if the entire chain is trusted (or empty).
//
// The chain may arrive as multiple header values; net/http preserves
// header repetition. Each value may itself be comma-separated. Flatten
// across both shapes.
func walkXFF(headers []string, peer netip.Addr, trusted []netip.Prefix) netip.Addr {
	chain := make([]string, 0, len(headers))
	for _, h := range headers {
		for _, e := range strings.Split(h, ",") {
			e = strings.TrimSpace(e)
			if e != "" {
				chain = append(chain, e)
			}
		}
	}
	for i := len(chain) - 1; i >= 0; i-- {
		addr, err := netip.ParseAddr(chain[i])
		if err != nil {
			// Malformed entry — stop walking. Don't trust anything we can't
			// parse, and don't keep popping past it.
			return peer
		}
		if !isTrusted(addr, trusted) {
			return addr
		}
	}
	return peer
}

func isTrusted(addr netip.Addr, trusted []netip.Prefix) bool {
	for _, p := range trusted {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// parseHostPort handles both `host:port` and bracketed IPv6 `[::1]:port`.
// Returns (zero, false) for malformed input.
func parseHostPort(remoteAddr string) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		// Some test paths leave RemoteAddr as a bare host — try parsing direct.
		if addr, err := netip.ParseAddr(remoteAddr); err == nil {
			return addr, true
		}
		return netip.Addr{}, false
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr, true
}

// joinHostPort rebuilds a `host:port` string preserving the port from the
// original RemoteAddr. If the port can't be extracted, falls back to the
// host alone — better than a malformed address.
func joinHostPort(addr netip.Addr, originalRemoteAddr string) string {
	_, port, err := net.SplitHostPort(originalRemoteAddr)
	if err != nil {
		return addr.String()
	}
	return net.JoinHostPort(addr.String(), port)
}
