package server

import (
	"context"

	"zymobrew/internal/config"
)

// SetMailerForTest overrides the SMTP sender so tests can assert on outbound
// mail (and extract email-change tokens) without a live relay. Test-only:
// this file is compiled into the package's test binary, not the library.
func (s *Server) SetMailerForTest(fn func(ctx context.Context, cfg config.Config, to, subject, body string) error) {
	s.sendMail = fn
}
