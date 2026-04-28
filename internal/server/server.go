package server

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/time/rate"

	"zymobrew/internal/config"
	"zymobrew/internal/queries"
	"zymobrew/internal/ratelimit"
)

type Server struct {
	pool    *pgxpool.Pool
	cfg     config.Config
	queries *queries.Queries
	handler http.Handler

	// Auth-path rate limiters. authIP gates /api/auth/{register,login} per
	// client IP; loginUser additionally gates /api/auth/login per identifier
	// so a single legitimate IP can't hammer one account.
	authIP    *ratelimit.Limiter
	loginUser *ratelimit.Limiter
}

func New(pool *pgxpool.Pool, cfg config.Config) *Server {
	s := &Server{
		pool:      pool,
		cfg:       cfg,
		queries:   queries.New(pool),
		authIP:    ratelimit.New(rate.Every(2*time.Second), 10, 30*time.Minute),
		loginUser: ratelimit.New(rate.Every(12*time.Second), 5, 30*time.Minute),
	}
	s.handler = s.routes()
	return s
}

func (s *Server) routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))
	r.Use(s.authMiddleware)

	r.Get("/healthz", s.healthz)
	r.Get("/readyz", s.readyz)

	r.Route("/api", func(r chi.Router) {
		r.Use(maxBodyBytes(1 << 20)) // 1 MiB ceiling on JSON bodies
		r.Route("/auth", func(r chi.Router) {
			r.Group(func(r chi.Router) {
				r.Use(s.ipRateLimit(s.authIP))
				r.Post("/register", s.handleRegister)
				r.Post("/login", s.handleLogin)
			})
			r.Post("/logout", s.handleLogout)
			r.With(s.requireAuth).Get("/me", s.handleMe)
		})
		r.Route("/users", func(r chi.Router) {
			r.With(s.requireAuth).Patch("/me", s.handleUpdateProfile)
			r.With(s.requireAuth).Post("/me/password", s.handleChangePassword)
			r.Get("/{username}", s.handleGetProfile)
		})
		r.Route("/recipes", func(r chi.Router) {
			r.Get("/", s.handleListRecipes)
			r.With(s.requireAuth).Post("/", s.handleCreateRecipe)
			r.With(s.requireAuth).Get("/mine", s.handleListMyRecipes)
			r.Route("/{id}", func(r chi.Router) {
				r.Get("/", s.handleGetRecipe)
				r.With(s.requireAuth).Patch("/", s.handleUpdateRecipe)
				r.With(s.requireAuth).Delete("/", s.handleDeleteRecipe)
				r.With(s.requireAuth).Post("/fork", s.handleForkRecipe)
				r.Get("/revisions", s.handleListRevisions)
				r.Get("/revisions/{rev}", s.handleGetRevision)
				r.Get("/comments", s.handleListComments)
				r.With(s.requireAuth).Post("/comments", s.handleCreateComment)
				r.With(s.requireAuth).Delete("/comments/{commentId}", s.handleDeleteComment)
				r.With(s.requireAuth).Post("/like", s.handleLikeRecipe)
				r.With(s.requireAuth).Delete("/like", s.handleUnlikeRecipe)
				r.Get("/reminder-templates", s.handleListReminderTemplates)
				r.With(s.requireAuth).Post("/reminder-templates", s.handleCreateReminderTemplate)
				r.With(s.requireAuth).Patch("/reminder-templates/{templateId}", s.handleUpdateReminderTemplate)
				r.With(s.requireAuth).Delete("/reminder-templates/{templateId}", s.handleDeleteReminderTemplate)
			})
		})
		r.Route("/batches", func(r chi.Router) {
			r.Use(s.requireAuth)
			r.Post("/", s.handleCreateBatch)
			r.Get("/", s.handleListBatches)
			r.Route("/{id}", func(r chi.Router) {
				r.Get("/", s.handleGetBatch)
				r.Patch("/", s.handleUpdateBatch)
				r.Delete("/", s.handleDeleteBatch)
				r.Post("/readings", s.handleCreateReading)
				r.Get("/readings", s.handleListReadings)
				r.Post("/events", s.handleCreateBatchEvent)
				r.Get("/events", s.handleListBatchEvents)
				r.Post("/tasting-notes", s.handleCreateTastingNote)
				r.Get("/tasting-notes", s.handleListTastingNotes)
				r.Post("/reminders", s.handleCreateReminder)
				r.Get("/reminders", s.handleListReminders)
				r.Patch("/reminders/{reminderId}", s.handleUpdateReminder)
				r.Delete("/reminders/{reminderId}", s.handleDeleteReminder)
			})
		})
		r.Route("/notifications", func(r chi.Router) {
			r.Use(s.requireAuth)
			r.Get("/", s.handleListNotifications)
			r.Post("/read-all", s.handleMarkAllNotificationsRead)
			r.Post("/{id}/read", s.handleMarkNotificationRead)
			r.Get("/prefs", s.handleGetNotificationPrefs)
			r.Patch("/prefs", s.handleUpdateNotificationPrefs)
		})
		r.Route("/push", func(r chi.Router) {
			r.Get("/public-key", s.handleGetVAPIDPublicKey)
			r.With(s.requireAuth).Post("/subscribe", s.handleSubscribePush)
			r.With(s.requireAuth).Post("/unsubscribe", s.handleUnsubscribePush)
		})
	})
	return r
}

func maxBodyBytes(n int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, n)
			next.ServeHTTP(w, r)
		})
	}
}

// ipRateLimit returns a middleware that consumes a token from `limiter`
// keyed by the client's IP. Trust assumption: chi's middleware.RealIP has
// already canonicalised X-Forwarded-For into r.RemoteAddr. Behind a proxy
// that's correct; directly exposed, this trusts an attacker-controlled
// header. Tighten when we add an explicit trusted-proxy config.
func (s *Server) ipRateLimit(limiter *ratelimit.Limiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !limiter.Allow(clientIP(r)) {
				w.Header().Set("Retry-After", "60")
				writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many requests"})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// clientIP strips the port from r.RemoteAddr. Falls back to the raw value
// if SplitHostPort fails (e.g. unix socket address shapes).
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

// Run starts the HTTP server. It returns when ctx is cancelled or
// ListenAndServe returns an error.
func (s *Server) Run(ctx context.Context, addr string) error {
	srv := &http.Server{Addr: addr, Handler: s.handler, ReadHeaderTimeout: 10 * time.Second}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	if s.pool == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "no db"})
		return
	}
	if err := s.pool.Ping(r.Context()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "db unavailable", "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
