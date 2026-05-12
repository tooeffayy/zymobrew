package config

import (
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"strings"

	"zymobrew/internal/storage"
)

type InstanceMode string

const (
	ModeSingleUser InstanceMode = "single_user"
	ModeClosed     InstanceMode = "closed"
	ModeOpen       InstanceMode = "open"
)

type Config struct {
	DatabaseURL     string
	ListenAddr      string
	InstanceMode    InstanceMode
	AutoMigrate     bool
	CookieSecure    bool
	VAPIDPublicKey  string
	VAPIDPrivateKey string
	VAPIDSubject    string

	// AppriseAPIURL is the base URL of an Apprise API sidecar
	// (e.g. http://apprise:8000). Empty disables external-channel delivery
	// — the dispatcher still creates in-app notifications. Per-user routing
	// is supplied by each user's notification_prefs.apprise_url.
	AppriseAPIURL string

	// AppriseAllowWebhookSchemes opts the instance into accepting Apprise
	// URLs whose scheme is a generic webhook target — http, https, json,
	// jsons, xml, xmls, form, forms. These let the caller fully control the
	// outbound URL the Apprise sidecar fetches, which is an SSRF primitive
	// when the sidecar shares a network with internal services. Off by
	// default; flip on only when the operator trusts every authenticated
	// user not to weaponize the sidecar.
	AppriseAllowWebhookSchemes bool

	// AppriseAllowedHostCIDRs lets the operator punch holes in the default
	// internal-range block — addresses falling into these prefixes are
	// allowed past the host check (e.g. an in-cluster ntfy server at
	// 10.42.0.10/32). Empty = no holes; the full default-blocked set
	// applies. See internal/apprise.defaultBlockedHostCIDRs for the
	// canonical set.
	AppriseAllowedHostCIDRs []netip.Prefix

	// SMTP delivery for users who don't want to run an Apprise sidecar.
	// Instance-wide relay — operator sets these once; each user opts in via
	// notification_prefs.email_enabled and reminders go to users.email.
	// SMTPHost empty = SMTP disabled (the toggle still flips but no mail is
	// sent). TLSMode is one of "starttls" | "tls" | "none"; default starttls
	// is the right answer for ~all modern relays on 587.
	SMTPHost     string
	SMTPPort     int
	SMTPUsername string
	SMTPPassword string
	SMTPFrom     string
	SMTPTLSMode  string

	// Primary storage — used for user-export archives. The local backend
	// roots files under StorageLocalPath; user exports specifically live
	// under the `tmp/exports/` subtree to convey their ephemeral lifecycle.
	StorageBackend   string // "local" | "s3"
	StorageLocalPath string
	S3Endpoint       string
	S3Region         string
	S3Bucket         string
	S3AccessKey      string
	S3SecretKey      string

	// Admin-backup storage — defined separately so backups can target a
	// different backend / location (e.g. off-site S3 while user exports
	// stay on local NAS). Each field falls back to the corresponding
	// STORAGE_/S3_ counterpart when the BACKUP_/BACKUP_S3_ env var is unset,
	// so the default deployment still has both pipelines on one backend.
	BackupBackend     string // "local" | "s3"
	BackupLocalPath   string
	BackupS3Endpoint  string
	BackupS3Region    string
	BackupS3Bucket    string
	BackupS3AccessKey string
	BackupS3SecretKey string

	BackupRetentionDays int

	// TrustedProxies is the set of CIDRs whose X-Forwarded-For header values
	// we honor. Empty means trust no upstream — the raw connection IP wins
	// and any XFF header is ignored. See internal/server/realip.go.
	TrustedProxies []netip.Prefix
}

func Load() (Config, error) {
	cfg := Config{
		DatabaseURL:     os.Getenv("DATABASE_URL"),
		ListenAddr:      getenv("LISTEN_ADDR", ":8080"),
		InstanceMode:    InstanceMode(getenv("INSTANCE_MODE", string(ModeSingleUser))),
		AutoMigrate:     getenvBool("AUTO_MIGRATE", true),
		CookieSecure:    getenvBool("COOKIE_SECURE", false),
		VAPIDPublicKey:  os.Getenv("VAPID_PUBLIC_KEY"),
		VAPIDPrivateKey: os.Getenv("VAPID_PRIVATE_KEY"),
		VAPIDSubject:    getenv("VAPID_SUBJECT", "mailto:admin@localhost"),

		AppriseAPIURL:              strings.TrimRight(os.Getenv("APPRISE_API_URL"), "/"),
		AppriseAllowWebhookSchemes: getenvBool("APPRISE_ALLOW_WEBHOOK_SCHEMES", false),

		SMTPHost:     os.Getenv("SMTP_HOST"),
		SMTPPort:     getenvInt("SMTP_PORT", 587),
		SMTPUsername: os.Getenv("SMTP_USERNAME"),
		SMTPPassword: os.Getenv("SMTP_PASSWORD"),
		SMTPFrom:     os.Getenv("SMTP_FROM"),
		SMTPTLSMode:  getenv("SMTP_TLS_MODE", "starttls"),

		StorageBackend:   getenv("STORAGE_BACKEND", "local"),
		StorageLocalPath: getenv("STORAGE_LOCAL_PATH", "./data"),
		S3Endpoint:       os.Getenv("S3_ENDPOINT"),
		S3Region:         getenv("S3_REGION", "us-east-1"),
		S3Bucket:         os.Getenv("S3_BUCKET"),
		S3AccessKey:      os.Getenv("S3_ACCESS_KEY"),
		S3SecretKey:      os.Getenv("S3_SECRET_KEY"),

		// BACKUP_* falls back to the matching STORAGE_/S3_ env if unset.
		BackupBackend:     os.Getenv("BACKUP_BACKEND"),
		BackupLocalPath:   os.Getenv("BACKUP_LOCAL_PATH"),
		BackupS3Endpoint:  os.Getenv("BACKUP_S3_ENDPOINT"),
		BackupS3Region:    os.Getenv("BACKUP_S3_REGION"),
		BackupS3Bucket:    os.Getenv("BACKUP_S3_BUCKET"),
		BackupS3AccessKey: os.Getenv("BACKUP_S3_ACCESS_KEY"),
		BackupS3SecretKey: os.Getenv("BACKUP_S3_SECRET_KEY"),

		BackupRetentionDays: getenvInt("BACKUP_RETENTION_DAYS", 30),
	}
	if cfg.DatabaseURL == "" {
		return cfg, fmt.Errorf("DATABASE_URL is required")
	}
	prefixes, err := parseTrustedProxies(os.Getenv("TRUSTED_PROXIES"))
	if err != nil {
		return cfg, err
	}
	cfg.TrustedProxies = prefixes
	allowedHostCIDRs, err := parseCIDRList("APPRISE_ALLOWED_HOST_CIDRS", os.Getenv("APPRISE_ALLOWED_HOST_CIDRS"))
	if err != nil {
		return cfg, err
	}
	cfg.AppriseAllowedHostCIDRs = allowedHostCIDRs
	switch cfg.InstanceMode {
	case ModeSingleUser, ModeClosed, ModeOpen:
	default:
		return cfg, fmt.Errorf("invalid INSTANCE_MODE %q (want single_user|closed|open)", cfg.InstanceMode)
	}
	// Bounded so the int32 cast in DeleteExpiredAdminBackups can't wrap; a
	// negative cutoff would push the deletion threshold into the future and
	// wipe every admin backup on the next dispatcher tick.
	if cfg.BackupRetentionDays < 1 || cfg.BackupRetentionDays > 36500 {
		return cfg, fmt.Errorf("invalid BACKUP_RETENTION_DAYS %d (want 1..36500)", cfg.BackupRetentionDays)
	}
	switch cfg.SMTPTLSMode {
	case "starttls", "tls", "none":
	default:
		return cfg, fmt.Errorf("invalid SMTP_TLS_MODE %q (want starttls|tls|none)", cfg.SMTPTLSMode)
	}
	return cfg, nil
}

// PrimaryStorage returns the BackendConfig for the primary store (user
// exports + general application data).
func (c Config) PrimaryStorage() storage.BackendConfig {
	return storage.BackendConfig{
		Backend:     c.StorageBackend,
		LocalPath:   c.StorageLocalPath,
		S3Endpoint:  c.S3Endpoint,
		S3Region:    c.S3Region,
		S3Bucket:    c.S3Bucket,
		S3AccessKey: c.S3AccessKey,
		S3SecretKey: c.S3SecretKey,
	}
}

// BackupStorage returns the BackendConfig for the admin-backup store. Each
// field falls back to the primary store's counterpart when its BACKUP_* env
// var is unset, so a deployment that doesn't configure BACKUP_* keeps both
// pipelines on a single backend.
func (c Config) BackupStorage() storage.BackendConfig {
	return storage.BackendConfig{
		Backend:     coalesce(c.BackupBackend, c.StorageBackend),
		LocalPath:   coalesce(c.BackupLocalPath, c.StorageLocalPath),
		S3Endpoint:  coalesce(c.BackupS3Endpoint, c.S3Endpoint),
		S3Region:    coalesce(c.BackupS3Region, c.S3Region),
		S3Bucket:    coalesce(c.BackupS3Bucket, c.S3Bucket),
		S3AccessKey: coalesce(c.BackupS3AccessKey, c.S3AccessKey),
		S3SecretKey: coalesce(c.BackupS3SecretKey, c.S3SecretKey),
	}
}

func coalesce(primary, fallback string) string {
	if primary != "" {
		return primary
	}
	return fallback
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getenvBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func getenvInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// parseTrustedProxies parses a comma-separated list of CIDRs (e.g.
// "10.0.0.0/8,fd00::/8"). Empty input → empty slice (trust nothing). A bare
// IP is accepted and treated as a /32 or /128.
func parseTrustedProxies(raw string) ([]netip.Prefix, error) {
	return parseCIDRList("TRUSTED_PROXIES", raw)
}

// parseCIDRList parses the shared "comma-separated CIDRs, bare IPs treated
// as /32 or /128" format used by both TRUSTED_PROXIES and the Apprise
// punch-hole list. envName is folded into error messages so the operator
// knows which knob to fix.
func parseCIDRList(envName, raw string) ([]netip.Prefix, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	out := make([]netip.Prefix, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if strings.Contains(p, "/") {
			pre, err := netip.ParsePrefix(p)
			if err != nil {
				return nil, fmt.Errorf("%s: invalid CIDR %q: %w", envName, p, err)
			}
			out = append(out, pre)
			continue
		}
		addr, err := netip.ParseAddr(p)
		if err != nil {
			return nil, fmt.Errorf("%s: invalid address %q: %w", envName, p, err)
		}
		out = append(out, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return out, nil
}
