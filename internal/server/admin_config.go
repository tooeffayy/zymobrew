package server

import "net/http"

// adminConfigView is the operator-facing config summary served at
// GET /api/admin/config. It exposes ONLY booleans, enums, and counts —
// plus the S3 access key (an identifier) behind a fixed-width tail mask.
// True secrets (DB URL, VAPID private key, SMTP password, S3 secret key)
// are reported solely as "_set" booleans.
//
// Every field is assigned explicitly in the handler; we never marshal
// config.Config, so a field added to Config later can't silently leak here.
// TestAdminConfig_NoSecretsLeak guards against regressions.
type adminConfigView struct {
	InstanceMode string `json:"instance_mode"`
	AutoMigrate  bool   `json:"auto_migrate"`
	CookieSecure bool   `json:"cookie_secure"`

	// "configured" = operator-level env present. Mirrors the dispatcher's own
	// predicates so this can't drift from what actually gates delivery.
	// Per-user opt-in toggles live on notification_prefs, not here.
	PushConfigured    bool `json:"push_configured"`
	AppriseConfigured bool `json:"apprise_configured"`
	EmailConfigured   bool `json:"email_configured"`

	AppriseAllowWebhookSchemes  bool `json:"apprise_allow_webhook_schemes"`
	AppriseAllowedHostCIDRCount int  `json:"apprise_allowed_host_cidr_count"`

	SMTPTLSMode string `json:"smtp_tls_mode,omitempty"`

	StorageBackend      string `json:"storage_backend"`
	BackupBackend       string `json:"backup_backend"`
	BackupRetentionDays int    `json:"backup_retention_days"`

	TrustedProxyCount int `json:"trusted_proxy_count"`

	// Secret presence. Booleans for true secrets; a last-4 tail mask for the
	// S3 access key ID (an identifier AWS itself displays). Never raw values.
	DatabaseURLSet     bool   `json:"database_url_set"`
	VAPIDPrivateKeySet bool   `json:"vapid_private_key_set"`
	SMTPPasswordSet    bool   `json:"smtp_password_set"`
	S3SecretKeySet     bool   `json:"s3_secret_key_set"`
	S3AccessKeyMasked  string `json:"s3_access_key_masked,omitempty"`
}

func isSet(v string) bool { return v != "" }

// maskTail reveals the last `keep` chars behind a fixed-width prefix. The
// prefix width is constant so the output never encodes the value's true
// length. Empty input → "". Use only for identifier-class values (e.g. an
// S3 access key ID), never true secrets.
func maskTail(v string, keep int) string {
	if v == "" {
		return ""
	}
	if len(v) <= keep {
		return "••••"
	}
	return "••••" + v[len(v)-keep:]
}

func (s *Server) handleAdminConfig(w http.ResponseWriter, r *http.Request) {
	c := s.cfg
	primary := c.PrimaryStorage()
	backup := c.BackupStorage() // resolves the BACKUP_*→STORAGE_* fallback

	view := adminConfigView{
		InstanceMode: string(c.InstanceMode),
		AutoMigrate:  c.AutoMigrate,
		CookieSecure: c.CookieSecure,

		PushConfigured:    c.VAPIDPublicKey != "" && c.VAPIDPrivateKey != "",
		AppriseConfigured: c.AppriseAPIURL != "",
		EmailConfigured:   c.SMTPHost != "" && c.SMTPFrom != "",

		AppriseAllowWebhookSchemes:  c.AppriseAllowWebhookSchemes,
		AppriseAllowedHostCIDRCount: len(c.AppriseAllowedHostCIDRs),

		StorageBackend:      primary.Backend,
		BackupBackend:       backup.Backend,
		BackupRetentionDays: c.BackupRetentionDays,

		TrustedProxyCount: len(c.TrustedProxies),

		DatabaseURLSet:     isSet(c.DatabaseURL),
		VAPIDPrivateKeySet: isSet(c.VAPIDPrivateKey),
		SMTPPasswordSet:    isSet(c.SMTPPassword),
		S3SecretKeySet:     isSet(primary.S3SecretKey),
		S3AccessKeyMasked:  maskTail(primary.S3AccessKey, 4),
	}
	if view.EmailConfigured {
		view.SMTPTLSMode = c.SMTPTLSMode
	}
	writeJSON(w, http.StatusOK, view)
}
