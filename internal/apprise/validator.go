// Package apprise validates user-supplied Apprise URLs before they hit the
// Apprise sidecar.
//
// Why this exists. The Apprise sidecar runs in the operator's network and
// will issue outbound requests using whatever URL we hand it. Several
// schemes Apprise supports are generic webhook plugins
// (http/https/json/jsons/xml/xmls/form/forms) that POST to an arbitrary
// user-supplied host — that turns the sidecar into an SSRF primitive
// reachable by any authenticated user. Even non-webhook schemes (ntfy://,
// mailto://, matrix://, gotify://, xmpp://, …) carry a host component for
// self-hosted variants, so `ntfy://10.0.0.1/topic` would still reach an
// internal address.
//
// The validator enforces two layers:
//
//  1. **Scheme policy.** Generic-webhook schemes are rejected unless the
//     operator opts in (AllowWebhookSchemes). All other Apprise schemes are
//     accepted at this layer.
//
//  2. **Host policy.** Whenever the URL has a host that resolves to an IP
//     in the default-blocked set (RFC1918, loopback, link-local, IMDS,
//     IPv6 ULA/link-local, multicast, doc/test ranges, etc.) the URL is
//     rejected. Operators can punch holes for legitimate internal
//     destinations via AllowedHostCIDRs.
//
// Token-style hosts (e.g. tgram://botToken/ChatID, discord://id/token) fail
// DNS resolution and are allowed through — Apprise would parse the host as
// a credential, not a network address, and there's no SSRF vector. We do
// not maintain a per-scheme allowlist because Apprise has 100+ plugins and
// the set drifts; the host check covers the cases that actually matter.
//
// DNS resolution is best-effort. A malicious operator of the destination
// can still rebind DNS between validation and the sidecar's fetch (TOCTOU);
// preventing that would require pinning the sidecar's resolver. The
// validator catches the common cases — literal-IP submissions and
// hostnames that already resolve into private space.
package apprise

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// ErrInvalid is returned for any policy-violating Apprise URL. Wrapped via
// fmt.Errorf with %w so callers can errors.Is.
var ErrInvalid = errors.New("apprise_url invalid")

// MaxURLLength caps Apprise URLs at the same length the schema/UI accept.
// 2048 leaves room for long mailto:// payloads with mid-length tokens
// without inviting megabyte-scale stuff into the prefs column.
const MaxURLLength = 2048

// Validator enforces the scheme + host policy. Zero value is unsafe — use
// NewValidator. Safe for concurrent use; immutable after construction.
type Validator struct {
	allowWebhookSchemes bool
	allowedHostCIDRs    []netip.Prefix
	resolver            *net.Resolver
}

// NewValidator builds a Validator from operator policy. allowedHostCIDRs is
// the punch-hole list — addresses that match these prefixes bypass the
// default block. Pass a nil/empty list when there's no override.
func NewValidator(allowWebhookSchemes bool, allowedHostCIDRs []netip.Prefix) *Validator {
	return &Validator{
		allowWebhookSchemes: allowWebhookSchemes,
		allowedHostCIDRs:    allowedHostCIDRs,
		resolver:            net.DefaultResolver,
	}
}

// deniedWebhookSchemes is the set of Apprise schemes that issue outbound
// requests to a fully user-controlled URL. Apprise documents them as
// "custom JSON/XML/form" notification plugins. They are the SSRF primitive
// we close by default.
var deniedWebhookSchemes = map[string]bool{
	"http": true, "https": true,
	"json": true, "jsons": true,
	"xml": true, "xmls": true,
	"form": true, "forms": true,
}

// Validate runs the full policy check. Returns nil if the URL is acceptable
// under the current policy; otherwise an error wrapping ErrInvalid with a
// user-readable message (safe to surface in 422 responses).
//
// ctx bounds DNS resolution. Pass the request context.
func (v *Validator) Validate(ctx context.Context, raw string) error {
	if len(raw) > MaxURLLength {
		return fmt.Errorf("%w: too long (max %d bytes)", ErrInvalid, MaxURLLength)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrInvalid, err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme == "" {
		return fmt.Errorf("%w: must look like scheme://...", ErrInvalid)
	}
	if deniedWebhookSchemes[scheme] && !v.allowWebhookSchemes {
		return fmt.Errorf("%w: scheme %q is a generic webhook target and is disabled on this instance (set APPRISE_ALLOW_WEBHOOK_SCHEMES=true to allow)", ErrInvalid, scheme)
	}
	host := u.Hostname()
	if host == "" {
		return nil
	}
	return v.checkHost(ctx, host)
}

// checkHost rejects hosts that resolve to an address in the default-blocked
// set, unless the address falls into the operator's punch-hole CIDR list.
// Hostnames that don't resolve are allowed through — they're either
// token-style "hosts" (tgram://, discord://) or genuine typos, and either
// way no SSRF reaches the network.
func (v *Validator) checkHost(ctx context.Context, host string) error {
	if addr, err := netip.ParseAddr(host); err == nil {
		return v.checkAddr(addr.Unmap(), host)
	}
	resolveCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	ips, err := v.resolver.LookupIP(resolveCtx, "ip", host)
	if err != nil {
		// Unresolvable — either a token-style host (tgram://, discord://)
		// or a typo. Either way Apprise won't reach the network on the
		// SSRF vector we care about, so let it through.
		return nil
	}
	for _, ip := range ips {
		addr, ok := netip.AddrFromSlice(ip)
		if !ok {
			continue
		}
		if err := v.checkAddr(addr.Unmap(), host); err != nil {
			return err
		}
	}
	return nil
}

func (v *Validator) checkAddr(addr netip.Addr, host string) error {
	// Punch-hole list wins: an operator that explicitly trusts a CIDR can
	// route Apprise destinations into it even if it falls in default-blocked
	// space.
	for _, p := range v.allowedHostCIDRs {
		if p.Contains(addr) {
			return nil
		}
	}
	for _, p := range defaultBlockedHostCIDRs {
		if p.Contains(addr) {
			return fmt.Errorf("%w: host %q resolves to a blocked range (%s); ask your administrator to add it to APPRISE_ALLOWED_HOST_CIDRS", ErrInvalid, host, addr)
		}
	}
	return nil
}

// defaultBlockedHostCIDRs covers the canonical "not on the public internet"
// IPv4/IPv6 ranges. Anything in here is rejected unless the operator has
// punched a hole via AllowedHostCIDRs.
var defaultBlockedHostCIDRs = mustParsePrefixes(
	// IPv4 — RFC 1122 / 1918 / 3927 / 5735 / 6598 / 6890.
	"0.0.0.0/8",          // "this network"
	"10.0.0.0/8",         // RFC1918 private
	"100.64.0.0/10",      // CGNAT
	"127.0.0.0/8",        // loopback
	"169.254.0.0/16",     // link-local incl. cloud IMDS at 169.254.169.254
	"172.16.0.0/12",      // RFC1918 private
	"192.0.0.0/24",       // IETF protocol assignments
	"192.0.2.0/24",       // TEST-NET-1
	"192.168.0.0/16",     // RFC1918 private
	"198.18.0.0/15",      // benchmarking
	"198.51.100.0/24",    // TEST-NET-2
	"203.0.113.0/24",     // TEST-NET-3
	"224.0.0.0/4",        // multicast
	"240.0.0.0/4",        // reserved
	"255.255.255.255/32", // limited broadcast
	// IPv6 — RFC 4193 / 4291 / 6890.
	"::/128",        // unspecified
	"::1/128",       // loopback
	"64:ff9b::/96",  // IPv4/IPv6 translation
	"100::/64",      // discard prefix
	"2001:db8::/32", // documentation
	"fc00::/7",      // unique local
	"fe80::/10",     // link-local
	"ff00::/8",      // multicast
)

func mustParsePrefixes(cidrs ...string) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(cidrs))
	for _, c := range cidrs {
		p, err := netip.ParsePrefix(c)
		if err != nil {
			panic(fmt.Sprintf("apprise: invalid default blocked CIDR %q: %v", c, err))
		}
		out = append(out, p)
	}
	return out
}
