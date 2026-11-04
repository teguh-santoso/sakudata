package api

import (
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// middleware wires security headers, panic recovery, request logging and
// brute-force protection for the login endpoints.
func (s *Server) middleware(next http.Handler) http.Handler {
	return s.recoverer(s.securityHeaders(s.requestLog(s.loginLimiter(next))))
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("panic: %v (%s %s)", rec, r.Method, r.URL.Path)
				writeErr(w, http.StatusInternalServerError, "Internal server error.")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		if strings.HasPrefix(r.URL.Path, "/_") {
			h.Set("Content-Security-Policy",
				"default-src 'self'; img-src 'self' data: blob:; "+
					"style-src 'self' 'unsafe-inline'; script-src 'self'; "+
					"connect-src 'self'; frame-ancestors 'none'")
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			// Sensitive JSON must never be cached by browsers or CDNs.
			h.Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
			h.Set("CDN-Cache-Control", "no-store")
			h.Set("Cloudflare-CDN-Cache-Control", "no-store")
			h.Set("Pragma", "no-cache")
			h.Set("Expires", "0")
			h.Set("Access-Control-Allow-Origin", "*")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			h.Set("Access-Control-Allow-Methods", "GET, POST, PATCH, PUT, DELETE, OPTIONS")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (s *Server) requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(rec, r)
		log.Printf("%s %s %d %s", r.Method, r.URL.Path, rec.status, time.Since(start).Round(time.Millisecond))
	})
}

// --- login rate limiting (fixed window per client IP) ---

type rateEntry struct {
	count int
	reset time.Time
}

type rateLimiter struct {
	mu      sync.Mutex
	entries map[string]*rateEntry
	limit   int
	window  time.Duration
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{entries: map[string]*rateEntry{}, limit: limit, window: window}
}

func (l *rateLimiter) allow(key string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.entries[key]
	if !ok || now.After(e.reset) {
		l.entries[key] = &rateEntry{count: 1, reset: now.Add(l.window)}
		return true
	}
	if e.count >= l.limit {
		return false
	}
	e.count++
	return true
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func isLoginRequest(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	if r.URL.Path == "/api/admin/login" || r.URL.Path == "/api/admin/setup" {
		return true
	}
	return strings.HasPrefix(r.URL.Path, "/api/d/") && strings.HasSuffix(r.URL.Path, "/auth/login")
}

var loginLimiter = newRateLimiter(30, time.Minute)

func (s *Server) loginLimiter(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isLoginRequest(r) && !loginLimiter.allow(clientIP(r)) {
			writeErr(w, http.StatusTooManyRequests, "Too many login attempts. Try again later.")
			return
		}
		next.ServeHTTP(w, r)
	})
}
