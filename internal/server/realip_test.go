package server

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

// mustPrefixes parses the given CIDR strings or fails the test.
func mustPrefixes(t *testing.T, cidrs ...string) []netip.Prefix {
	t.Helper()
	out := make([]netip.Prefix, 0, len(cidrs))
	for _, c := range cidrs {
		p, err := netip.ParsePrefix(c)
		if err != nil {
			t.Fatalf("parse %q: %v", c, err)
		}
		out = append(out, p)
	}
	return out
}

// runRealIP threads a request through the realIP middleware and returns
// (resolved IP from context, rewritten r.RemoteAddr).
func runRealIP(t *testing.T, peer string, xff []string, trusted []netip.Prefix) (netip.Addr, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = peer
	for _, h := range xff {
		req.Header.Add("X-Forwarded-For", h)
	}

	var resolved netip.Addr
	var rebuiltRemote string
	mw := realIP(trusted)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resolved = clientIPFromContext(r.Context())
		rebuiltRemote = r.RemoteAddr
	}))
	mw.ServeHTTP(httptest.NewRecorder(), req)
	return resolved, rebuiltRemote
}

func TestRealIP_NoTrustedProxies_IgnoresXFF(t *testing.T) {
	got, _ := runRealIP(t, "203.0.113.5:1234", []string{"5.6.7.8"}, nil)
	want := netip.MustParseAddr("203.0.113.5")
	if got != want {
		t.Fatalf("got %v, want %v — XFF must be ignored when no proxies trusted", got, want)
	}
}

func TestRealIP_TrustedSingleHop(t *testing.T) {
	got, remote := runRealIP(t,
		"10.1.2.3:55000",
		[]string{"203.0.113.5"},
		mustPrefixes(t, "10.0.0.0/8"),
	)
	if want := netip.MustParseAddr("203.0.113.5"); got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
	if want := "203.0.113.5:55000"; remote != want {
		t.Fatalf("RemoteAddr rewrite = %q, want %q", remote, want)
	}
}

// TestRealIP_MultiHop_WalksChain is the load-bearing case: chain has the
// real client at the front, then two trusted proxies. The middleware must
// pop both trusted entries and stop at the real client. A future refactor
// that reads only one end of the chain breaks this test.
func TestRealIP_MultiHop_WalksChain(t *testing.T) {
	got, _ := runRealIP(t,
		"10.1.2.3:55000",
		[]string{"203.0.113.5, 10.0.0.7, 192.168.1.4"},
		mustPrefixes(t, "10.0.0.0/8", "192.168.0.0/16"),
	)
	if want := netip.MustParseAddr("203.0.113.5"); got != want {
		t.Fatalf("got %v, want %v — should walk past trusted proxies to real client", got, want)
	}
}

// TestRealIP_SpoofIgnored: peer is NOT in the trusted set, so the XFF
// header is entirely ignored. The connection peer wins. This is the test
// that confirms an attacker can't bypass the per-IP rate limiter by
// rotating XFF values.
func TestRealIP_SpoofIgnored(t *testing.T) {
	got, _ := runRealIP(t,
		"1.2.3.4:9000",
		[]string{"5.6.7.8"},
		mustPrefixes(t, "10.0.0.0/8"),
	)
	if want := netip.MustParseAddr("1.2.3.4"); got != want {
		t.Fatalf("got %v, want %v — untrusted peer's XFF must be ignored", got, want)
	}
}

func TestRealIP_ChainAllTrusted_FallsBackToPeer(t *testing.T) {
	got, _ := runRealIP(t,
		"10.1.2.3:55000",
		[]string{"10.0.0.7, 10.0.0.8"},
		mustPrefixes(t, "10.0.0.0/8"),
	)
	if want := netip.MustParseAddr("10.1.2.3"); got != want {
		t.Fatalf("got %v, want %v — fully-trusted chain falls back to peer", got, want)
	}
}

func TestRealIP_MalformedXFFEntry_StopsWalk(t *testing.T) {
	// Chain: real, garbage, trusted. Walking right-to-left we pop the
	// trusted entry, then hit garbage and bail rather than blindly
	// trusting whatever comes next.
	got, _ := runRealIP(t,
		"10.1.2.3:55000",
		[]string{"203.0.113.5, not-an-ip, 10.0.0.7"},
		mustPrefixes(t, "10.0.0.0/8"),
	)
	if want := netip.MustParseAddr("10.1.2.3"); got != want {
		t.Fatalf("got %v, want %v — malformed entry must stop the walk at peer", got, want)
	}
}

func TestRealIP_IPv6_Bracketed(t *testing.T) {
	got, remote := runRealIP(t,
		"[fd00::1]:55000",
		[]string{"2001:db8::1"},
		mustPrefixes(t, "fd00::/8"),
	)
	if want := netip.MustParseAddr("2001:db8::1"); got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
	if want := "[2001:db8::1]:55000"; remote != want {
		t.Fatalf("RemoteAddr rewrite = %q, want %q", remote, want)
	}
}

func TestRealIP_HeaderRepetition(t *testing.T) {
	// Some clients/proxies send XFF as multiple separate header values
	// instead of a single comma-joined value. Both must flatten the same.
	got, _ := runRealIP(t,
		"10.1.2.3:55000",
		[]string{"203.0.113.5", "10.0.0.7"},
		mustPrefixes(t, "10.0.0.0/8"),
	)
	if want := netip.MustParseAddr("203.0.113.5"); got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}
