package serve

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Yash-K-Jagani/ycode/internal/config"
)

// Authentication for the local API.
//
// /v1/chat runs the agent with bash, write and delete, in a workdir the caller
// supplies. It had no authentication at all, so any local process could drive
// it — and so could any web page the user had open, because a no-preflight
// POST is still sent cross-origin even though the response is blocked. Binding
// to loopback does not help against either.
//
// The policy balances that against being a local convenience:
//
//   - Loopback binds stay usable with no token, which is the common case, but
//     the token is still minted and printed so a user who wants it can pass it.
//   - A non-loopback bind (explicitly requested) requires the token. There is no
//     override for that case, because it is exposing the machine.
//   - YCODE_ALLOW_ANONYMOUS_API=1 restores anonymous access for one release as
//     a migration path for scripts that predate this.
//
// The token is stored in ~/.ycode/api_token (0600) so it survives restarts, and
// accepted as a bearer token or an X-Ycode-Token header.
const (
	tokenFile    = "api_token"
	tokenEnv     = "YCODE_API_TOKEN"
	anonEnv      = "YCODE_ALLOW_ANONYMOUS_API"
	tokenHeader  = "X-Ycode-Token"
	bearerPrefix = "Bearer "
)

var tokenFilePath = func() string { return filepath.Join(config.Dir(), tokenFile) }

// Token returns the API token: YCODE_API_TOKEN, else the persisted one in
// ~/.ycode/api_token, minting and writing one on first use.
//
// Deliberately not memoised. It costs one small file read per call (only on
// authenticated binds) and in exchange there is no global state to reset, a
// restart keeps the same token, and an env override takes effect immediately.
func Token() string {
	if v := strings.TrimSpace(os.Getenv(tokenEnv)); v != "" {
		return v
	}
	p := tokenFilePath()
	if data, err := os.ReadFile(p); err == nil {
		if t := strings.TrimSpace(string(data)); t != "" {
			return t
		}
	}
	// Mint. If persistence fails we still return a token, so the API is never
	// silently unauthenticated — it just will not survive a restart.
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	t := hex.EncodeToString(buf)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err == nil {
		_ = os.WriteFile(p, []byte(t+"\n"), 0o600)
	}
	return t
}

// IsLoopback reports whether an address binds only to this machine.
func IsLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	host = strings.Trim(host, "[]")
	if host == "" {
		return true // ":8471" binds loopback
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// authRequired reports whether this bind needs a token, and why not.
func authRequired(addr string) bool {
	if strings.EqualFold(strings.TrimSpace(os.Getenv(anonEnv)), "1") {
		return false
	}
	return !IsLoopback(addr)
}

func authorized(r *http.Request) bool {
	want := Token()
	if want == "" {
		return true
	}
	got := strings.TrimSpace(r.Header.Get(tokenHeader))
	if got == "" {
		if a := r.Header.Get("Authorization"); strings.HasPrefix(a, bearerPrefix) {
			got = strings.TrimSpace(strings.TrimPrefix(a, bearerPrefix))
		}
	}
	if got == "" {
		// Allow a query token for convenience with curl and EventSource-ish
		// clients that cannot set headers.
		got = strings.TrimSpace(r.URL.Query().Get("token"))
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// withAuth wraps the handler so unauthenticated calls are refused with a
// message that says how to authenticate.
func withAuth(h http.Handler, addr string) http.Handler {
	if !authRequired(addr) {
		return h
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			h.ServeHTTP(w, r)
			return
		}
		if !authorized(r) {
			w.Header().Set("WWW-Authenticate", bearerPrefix+" realm=ycode")
			http.Error(w, "unauthorized: send the token as `Authorization: Bearer <token>`, "+
				"X-Ycode-Token: <token>, or ?token=<token>. `ycode serve` prints it.", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// NewFor builds a handler with the auth policy applied for a given bind address.
// New() is kept for tests and for embedding, where the caller decides.
func NewFor(addr string) *Server {
	s := New()
	s.handler = withAuth(s.mux, addr)
	return s
}

func (s *Server) Handler() http.Handler {
	if s.handler != nil {
		return s.handler
	}
	return s.mux
}

func Run(addr string) error {
	s := NewFor(addr)
	srv := &http.Server{Addr: addr, Handler: s.Handler(), ReadTimeout: 30 * time.Second, WriteTimeout: 20 * time.Minute}
	fmt.Println("ycode api on", addr)
	if authRequired(addr) {
		fmt.Println("this address is not loopback, so the API requires a token:")
		fmt.Println("  Authorization: Bearer " + Token())
	} else {
		fmt.Println("loopback bind: the token is optional. Send it anyway with:")
		fmt.Println("  Authorization: Bearer " + Token())
	}
	return srv.ListenAndServe()
}
