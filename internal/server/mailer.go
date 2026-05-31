package server

import (
	"context"
	"fmt"
	"time"

	mail "github.com/wneessen/go-mail"

	"zymobrew/internal/config"
)

// mailSender delivers a single plaintext message through the instance relay.
// It's a field on Server (defaulting to sendEmailSMTP) so tests can inject a
// stub and assert on what would have been sent without a live SMTP server.
type mailSender func(ctx context.Context, cfg config.Config, toAddr, subject, body string) error

// sendEmailSMTP dials the configured SMTP relay and sends one plaintext
// message, returning the underlying error so callers can surface it. This is
// the synchronous, error-propagating counterpart to the reminder
// dispatcher's log-and-drop sendEmail; both share the same TLS/auth wiring.
func sendEmailSMTP(ctx context.Context, cfg config.Config, toAddr, subject, body string) error {
	opts := []mail.Option{
		mail.WithPort(cfg.SMTPPort),
		mail.WithTimeout(15 * time.Second),
	}
	switch cfg.SMTPTLSMode {
	case "tls":
		opts = append(opts, mail.WithSSL())
	case "none":
		opts = append(opts, mail.WithTLSPolicy(mail.NoTLS))
	default:
		opts = append(opts, mail.WithTLSPolicy(mail.TLSMandatory))
	}
	if cfg.SMTPUsername != "" {
		opts = append(opts,
			mail.WithSMTPAuth(mail.SMTPAuthPlain),
			mail.WithUsername(cfg.SMTPUsername),
			mail.WithPassword(cfg.SMTPPassword),
		)
	}
	client, err := mail.NewClient(cfg.SMTPHost, opts...)
	if err != nil {
		return fmt.Errorf("smtp client: %w", err)
	}
	msg := mail.NewMsg()
	if err := msg.From(cfg.SMTPFrom); err != nil {
		return fmt.Errorf("smtp from: %w", err)
	}
	if err := msg.To(toAddr); err != nil {
		return fmt.Errorf("smtp to: %w", err)
	}
	msg.Subject(subject)
	msg.SetBodyString(mail.TypeTextPlain, body)
	dialCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := client.DialAndSendWithContext(dialCtx, msg); err != nil {
		return fmt.Errorf("smtp send: %w", err)
	}
	return nil
}
