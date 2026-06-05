package apprise

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
)

// TestValidate_RejectsGenericWebhookSchemesByDefault asserts the primary
// SSRF closure: http/https/json/jsons/xml/xmls/form/forms are the schemes
// Apprise treats as fully user-controlled webhook targets, and we refuse
// them unless the operator opts in.
func TestValidate_RejectsGenericWebhookSchemesByDefault(t *testing.T) {
	v := NewValidator(false, nil)
	for _, scheme := range []string{"http", "https", "json", "jsons", "xml", "xmls", "form", "forms"} {
		raw := scheme + "://attacker.example.com/path"
		err := v.Validate(context.Background(), raw)
		if err == nil {
			t.Errorf("%s: expected rejection, got nil", scheme)
			continue
		}
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: expected ErrInvalid, got %T %v", scheme, err, err)
		}
		if !strings.Contains(err.Error(), scheme) {
			t.Errorf("%s: error %q should mention the scheme", scheme, err)
		}
	}
}

// TestValidate_AllowsWebhookSchemesWhenOperatorOptsIn confirms the opt-in
// path. Note: the scheme check passes, but the host check may still fire
// — tested separately.
func TestValidate_AllowsWebhookSchemesWhenOperatorOptsIn(t *testing.T) {
	v := NewValidator(true, nil)
	// 1.1.1.1 is public (not in any default-blocked range) so this should
	// pass cleanly.
	if err := v.Validate(context.Background(), "https://1.1.1.1/hook"); err != nil {
		t.Errorf("expected acceptance, got %v", err)
	}
}

// TestValidate_AllowsNamedServiceSchemes verifies the legitimate Apprise
// schemes still pass with default policy.
func TestValidate_AllowsNamedServiceSchemes(t *testing.T) {
	v := NewValidator(false, nil)
	cases := []string{
		// Token-style hosts that won't resolve.
		"tgram://bottoken_aaaaaaaaaaaaaaaaaaaaaaaaa/1234567890",
		"discord://1234567890/abcdef-token",
		"slack://botToken/Channel",
		"pover://user@apptoken",
	}
	for _, raw := range cases {
		if err := v.Validate(context.Background(), raw); err != nil {
			t.Errorf("%s: expected acceptance, got %v", raw, err)
		}
	}
}

// TestValidate_BlocksLiteralIMDS is the canonical SSRF case — even with
// webhook schemes allowed, the cloud metadata service address should be
// blocked by the host policy.
func TestValidate_BlocksLiteralIMDS(t *testing.T) {
	v := NewValidator(true /* webhooks allowed */, nil)
	err := v.Validate(context.Background(), "http://169.254.169.254/latest/meta-data/")
	if err == nil {
		t.Fatal("expected rejection of IMDS address, got nil")
	}
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid, got %T %v", err, err)
	}
}

// TestValidate_BlocksLiteralPrivateRanges sweeps the well-known private +
// loopback IPv4 ranges to confirm coverage.
func TestValidate_BlocksLiteralPrivateRanges(t *testing.T) {
	v := NewValidator(true, nil)
	cases := []string{
		"http://127.0.0.1/x",        // loopback
		"http://10.0.0.1/x",         // RFC1918
		"http://172.16.0.1/x",       // RFC1918
		"http://192.168.1.1/x",      // RFC1918
		"http://100.64.0.1/x",       // CGNAT
		"http://0.0.0.0/x",          // "this network"
		"http://255.255.255.255/x",  // limited broadcast
		"mailto://u:p@10.0.0.5/x",   // SMTP-via-private — different scheme, same host
		"ntfy://192.168.50.10/topic", // self-hosted ntfy on private LAN
	}
	for _, raw := range cases {
		if err := v.Validate(context.Background(), raw); err == nil {
			t.Errorf("%s: expected rejection, got nil", raw)
		}
	}
}

// TestValidate_BlocksLiteralIPv6Ranges covers IPv6 loopback/ULA/link-local
// and the IPv4-mapped form (which we Unmap before checking).
func TestValidate_BlocksLiteralIPv6Ranges(t *testing.T) {
	v := NewValidator(true, nil)
	cases := []string{
		"http://[::1]/x",            // loopback
		"http://[fe80::1]/x",        // link-local
		"http://[fc00::1]/x",        // ULA
		"http://[fd12:3456:789a::1]/x", // ULA
		"http://[::ffff:10.0.0.1]/x",   // IPv4-mapped 10.0.0.1
	}
	for _, raw := range cases {
		if err := v.Validate(context.Background(), raw); err == nil {
			t.Errorf("%s: expected rejection, got nil", raw)
		}
	}
}

// TestValidate_BlocksZoneScopedIPv6 is a regression test: netip.Prefix.Contains
// returns false for any address carrying an IPv6 zone identifier, so a literal
// like ::1%lo0 / fe80::1%eth0 would slip past every blocked-range check. We
// reject zone-bearing literals outright. The %25 is the URL-encoding of the %
// zone delimiter, which url.Parse + Hostname() decode back to a bare %.
func TestValidate_BlocksZoneScopedIPv6(t *testing.T) {
	v := NewValidator(true, nil)
	cases := []string{
		"ntfy://[::1%25lo0]/x",     // loopback, scoped to lo0
		"ntfy://[fe80::1%25eth0]/x", // link-local, scoped to eth0
		"http://[::1%25lo0]/x",     // same, generic-webhook scheme
	}
	for _, raw := range cases {
		if err := v.Validate(context.Background(), raw); err == nil {
			t.Errorf("%s: expected rejection of zone-scoped address, got nil", raw)
		}
	}
}

// TestValidate_AllowsCIDROverride verifies the admin punch-hole list: an
// operator who runs an internal ntfy server at 10.42.0.10 can permit it
// without disabling the wider block.
func TestValidate_AllowsCIDROverride(t *testing.T) {
	allowed := []netip.Prefix{netip.MustParsePrefix("10.42.0.0/24")}
	v := NewValidator(false, allowed)

	// In the punch-hole — accepted.
	if err := v.Validate(context.Background(), "ntfy://10.42.0.10/alerts"); err != nil {
		t.Errorf("expected acceptance of allowed CIDR, got %v", err)
	}
	// Still in the default-blocked range but NOT in the punch-hole — rejected.
	if err := v.Validate(context.Background(), "ntfy://10.99.0.1/alerts"); err == nil {
		t.Errorf("expected rejection of un-allowed private IP, got nil")
	}
}

// TestValidate_AllowsPublicLiteralAddress sanity-checks the not-private
// path. 1.1.1.1 is Cloudflare's resolver — clearly public — and should
// pass even without webhook schemes since ntfy:// is allowed by default.
func TestValidate_AllowsPublicLiteralAddress(t *testing.T) {
	v := NewValidator(false, nil)
	if err := v.Validate(context.Background(), "ntfy://1.1.1.1/topic"); err != nil {
		t.Errorf("expected acceptance of public IP, got %v", err)
	}
}

// TestValidate_RejectsLengthAndMissingScheme covers the structural checks
// retained from the original validator.
func TestValidate_RejectsLengthAndMissingScheme(t *testing.T) {
	v := NewValidator(true, nil)
	tooLong := "tgram://" + strings.Repeat("a", MaxURLLength)
	if err := v.Validate(context.Background(), tooLong); err == nil {
		t.Error("expected length rejection, got nil")
	}
	if err := v.Validate(context.Background(), "no-scheme-here"); err == nil {
		t.Error("expected missing-scheme rejection, got nil")
	}
}
