// Copyright (c) 2026 Michael D Henderson. All rights reserved.

// Package auth resolves the logged-in account from an HttpOnly session cookie.
//
// Sessions are opaque and server-side (see server/store): the cookie carries a
// random token, the database stores only its SHA-256, and revocation is a row
// delete. This package owns the things the store deliberately does not — the
// clock, token generation, and the HTTP cookie — and exposes:
//
//   - Manager.Middleware: looks up the cookie's session, enforces expiry (and,
//     optionally, IP binding), slides the session forward, and attaches the
//     resolved identity to the request context.
//   - IssueCookie / ClearCookie: for the (separate) login and logout handlers.
//   - FromContext: how downstream handlers read the resolved session.
//
// CSRF is NOT handled here; it is enforced at the edge by the stdlib
// http.CrossOriginProtection middleware wired up in the serve command.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/mdhender/ecv3/server/store"
)

// DefaultCookieName is the session cookie name. The "__Host-" prefix is a
// browser-enforced hardening: the cookie is accepted only when sent over HTTPS,
// with Path=/ and no Domain attribute, which pins it to the exact origin. We do
// not support older browsers, so relying on it is fine.
const DefaultCookieName = "__Host-ecv3_session"

// DefaultTTL is the sliding session lifetime if Config.TTL is left zero.
const DefaultTTL = 30 * 24 * time.Hour

// tokenBytes is the size of the raw session token before encoding (256 bits).
const tokenBytes = 32

// Config tunes session behavior.
type Config struct {
	// BindIP, when true, ties a session to the IP it was created with: a request
	// arriving from a different IP is treated as unauthenticated. When false the
	// IP is still recorded (audit) but never enforced — needed for clients whose
	// IP rotates (e.g. a VPN while travelling). Wired to `serve --session-bind-ip`,
	// which defaults to true.
	BindIP bool

	// TTL is the sliding session lifetime. Each authenticated request pushes the
	// expiry to now+TTL. Zero means DefaultTTL.
	TTL time.Duration

	// CookieName overrides DefaultCookieName (mainly for tests). Note the
	// "__Host-" prefix imposes Secure + Path=/ + no Domain.
	CookieName string

	// Secure sets the cookie's Secure attribute. It must be true in any real
	// deployment (and is required by the "__Host-" prefix); a test/plain-HTTP
	// harness may set it false with a non-prefixed CookieName.
	Secure bool

	// TrustedProxies lists the reverse proxies (by CIDR) that sit in front of
	// this server. X-Forwarded-For is honored ONLY when the request's direct
	// peer falls within one of these ranges; from any other source the header is
	// attacker-controlled and ignored (the direct peer IP is used instead).
	// Empty means trust no proxy: always use the direct peer. In ecv3's
	// deployment this is the loopback range (Caddy runs on the same host).
	TrustedProxies []netip.Prefix
}

// Manager resolves and maintains sessions against a store. Construct it with New.
type Manager struct {
	store *store.Store
	cfg   Config
	now   func() time.Time // injectable clock; defaults to time.Now
}

// New builds a Manager. A zero Config.TTL becomes DefaultTTL and an empty
// CookieName becomes DefaultCookieName.
func New(st *store.Store, cfg Config) *Manager {
	if cfg.TTL == 0 {
		cfg.TTL = DefaultTTL
	}
	if cfg.CookieName == "" {
		cfg.CookieName = DefaultCookieName
	}
	return &Manager{store: st, cfg: cfg, now: time.Now}
}

// Session is the identity resolved for a request. AccountID is the real,
// authenticated account (who owns the session and to whom actions are
// attributed). EffectiveAccountID is who the request acts as — the same as
// AccountID unless an admin is impersonating, in which case it is the target.
type Session struct {
	IDHash             string
	AccountID          int64
	EffectiveAccountID int64
	CurrentGameID      int64 // 0 = not in a game
	Impersonating      bool
}

// contextKey is unexported so only this package can set the session value.
type contextKey struct{}

// FromContext returns the Session attached by Middleware, if any. ok is false
// for unauthenticated requests.
func FromContext(ctx context.Context) (Session, bool) {
	s, ok := ctx.Value(contextKey{}).(Session)
	return s, ok
}

// Middleware resolves the session cookie and, on success, attaches a Session to
// the request context for downstream handlers (read via FromContext). It does
// NOT itself reject unauthenticated requests — that is the job of a per-route
// guard — so it can wrap public and private routes alike. A missing, unknown,
// expired, or (when BindIP is on) IP-mismatched cookie simply yields no
// attached session.
func (m *Manager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if sess, ok := m.resolve(w, r); ok {
			r = r.WithContext(context.WithValue(r.Context(), contextKey{}, sess))
		}
		next.ServeHTTP(w, r)
	})
}

// resolve reads the cookie and returns the resolved Session, refreshing it in
// the store. It returns ok=false (and, for an actually-stale cookie, clears it)
// when no valid session applies.
func (m *Manager) resolve(w http.ResponseWriter, r *http.Request) (Session, bool) {
	c, err := r.Cookie(m.cfg.CookieName)
	if err != nil || c.Value == "" {
		return Session{}, false
	}
	idHash := hashToken(c.Value)

	rec, found, err := m.store.GetSession(r.Context(), idHash)
	if err != nil {
		// Treat a lookup error as unauthenticated; do not clear the cookie (the
		// session may be fine and the DB merely briefly unavailable).
		return Session{}, false
	}

	now := m.now()
	ip := m.clientIP(r)

	// Unknown or expired => stale cookie. Clear it so the browser stops sending it.
	if !found || now.Unix() >= rec.ExpiresAt {
		ClearCookie(w, m.cfg)
		return Session{}, false
	}

	// Optional IP binding. A mismatch is not "stale" (the same cookie is valid
	// from its real IP), so we do NOT clear it — we just decline to authenticate.
	if m.cfg.BindIP && rec.IP != "" && rec.IP != ip {
		return Session{}, false
	}

	// Slide the session forward and refresh the recorded fingerprint. A failure
	// here is non-fatal: we still serve the request with the resolved identity.
	expires := now.Add(m.cfg.TTL).Unix()
	_ = m.store.TouchSession(r.Context(), idHash, ip, r.UserAgent(), now.Unix(), expires)

	return toSession(rec), true
}

// Create starts a new session for accountID, writes the cookie, and returns the
// stored Session. It is called by the login handler after credentials check out.
func (m *Manager) Create(ctx context.Context, w http.ResponseWriter, r *http.Request, accountID int64) (Session, error) {
	token, err := newToken()
	if err != nil {
		return Session{}, err
	}
	idHash := hashToken(token)

	now := m.now()
	expires := now.Add(m.cfg.TTL)
	if err := m.store.CreateSession(ctx, idHash, accountID, m.clientIP(r), r.UserAgent(), now.Unix(), expires.Unix(), now.Unix()); err != nil {
		return Session{}, err
	}

	IssueCookie(w, m.cfg, token, expires)
	return Session{IDHash: idHash, AccountID: accountID, EffectiveAccountID: accountID}, nil
}

// Destroy deletes the request's session (logout) and clears the cookie. A
// request without a session is a no-op.
func (m *Manager) Destroy(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
	defer ClearCookie(w, m.cfg)
	c, err := r.Cookie(m.cfg.CookieName)
	if err != nil || c.Value == "" {
		return nil
	}
	return m.store.DeleteSession(ctx, hashToken(c.Value))
}

// IssueCookie writes the session cookie carrying the raw token. Attributes:
// HttpOnly (no JS access), Secure (HTTPS only), SameSite=Lax, Path=/.
func IssueCookie(w http.ResponseWriter, cfg Config, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName(cfg),
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   cfg.Secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// ClearCookie expires the session cookie. The attributes (except Value/Expires)
// must match IssueCookie or the browser will not replace it.
func ClearCookie(w http.ResponseWriter, cfg Config) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName(cfg),
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   cfg.Secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func cookieName(cfg Config) string {
	if cfg.CookieName == "" {
		return DefaultCookieName
	}
	return cfg.CookieName
}

// toSession maps a stored row to the request-facing Session, collapsing the
// impersonation pointer into an effective identity.
func toSession(rec store.Session) Session {
	s := Session{
		IDHash:             rec.IDHash,
		AccountID:          rec.AccountID,
		EffectiveAccountID: rec.AccountID,
		CurrentGameID:      rec.CurrentGameID,
	}
	if rec.ImpersonatedAccountID != 0 {
		s.EffectiveAccountID = rec.ImpersonatedAccountID
		s.Impersonating = true
	}
	return s
}

// newToken returns a URL-safe random session token (256 bits of entropy).
func newToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// hashToken maps a raw token to the hex SHA-256 stored as the session id. The
// token has full entropy, so a plain (unsalted, un-stretched) hash is correct
// here — it only needs to be one-way and collision-resistant.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// clientIP returns the best-effort real client IP. If the request's direct peer
// is a trusted proxy (Config.TrustedProxies), it consults X-Forwarded-For so the
// IP recorded and (optionally) bound is the real client rather than the proxy;
// otherwise the direct peer is used and any X-Forwarded-For is ignored, since a
// header from an untrusted source is attacker-controlled.
//
// X-Forwarded-For reads "client, proxy1, proxy2" left to right — the rightmost
// entries are added by our own infra. We scan from the right and return the
// first address that is NOT a trusted proxy: the real client even behind a chain
// of proxies, and unspoofable since an attacker can only prepend left-hand
// entries that the scan never reaches.
func (m *Manager) clientIP(r *http.Request) string {
	peer := parseIP(r.RemoteAddr)
	if !peer.IsValid() {
		return r.RemoteAddr
	}
	if !m.isTrustedProxy(peer) {
		return peer.String()
	}
	parts := splitForwardedFor(r.Header.Values("X-Forwarded-For"))
	for i := len(parts) - 1; i >= 0; i-- {
		addr := parseIP(parts[i])
		if !addr.IsValid() || m.isTrustedProxy(addr) {
			continue
		}
		return addr.String()
	}
	return peer.String()
}

// isTrustedProxy reports whether addr falls within any configured trusted-proxy
// CIDR. The address is unmapped so an IPv4-mapped IPv6 form matches an IPv4 CIDR.
func (m *Manager) isTrustedProxy(addr netip.Addr) bool {
	addr = addr.Unmap()
	for _, p := range m.cfg.TrustedProxies {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// parseIP parses an address that may be bare ("1.2.3.4", "::1"), host:port
// ("1.2.3.4:80", "[::1]:80"), or IPv4-mapped IPv6. It returns an unmapped Addr,
// or the zero Addr on failure.
func parseIP(s string) netip.Addr {
	s = strings.TrimSpace(s)
	if s == "" {
		return netip.Addr{}
	}
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return ap.Addr().Unmap()
	}
	if a, err := netip.ParseAddr(s); err == nil {
		return a.Unmap()
	}
	if host, _, err := net.SplitHostPort(s); err == nil {
		if a, err := netip.ParseAddr(strings.TrimSpace(host)); err == nil {
			return a.Unmap()
		}
	}
	return netip.Addr{}
}

// splitForwardedFor flattens one-or-more X-Forwarded-For header values (each
// possibly comma-separated) into a trimmed, non-empty list, preserving order.
func splitForwardedFor(values []string) []string {
	var out []string
	for _, v := range values {
		for _, part := range strings.Split(v, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}
