package server

import (
	"crypto/rand"
	"encoding/base64"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	deviceCookie  = "hc_device"
	codeAlphabet  = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // no 0/O or 1/I to misread
	codeLength    = 8
	codeTTL       = 10 * time.Minute
	maxWrongCodes = 5
	cookieAge     = 400 * 24 * time.Hour // the longest a browser keeps a cookie
	seenEvery     = time.Minute
)

// pairing holds the one code hc pair handed out, and what the paired devices last did.
type pairing struct {
	mu      sync.Mutex
	code    string
	name    string
	expires time.Time
	wrong   int
	seen    map[string]time.Time
}

// remote reports a request from another machine: straight to the port when host is not loopback,
// or through a proxy such as tailscale serve, which connects from loopback and adds its headers.
// The Host header is whatever the client sent, and proves nothing.
func remote(r *http.Request) bool {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err != nil || !net.ParseIP(host).IsLoopback() {
		return true
	}
	for _, h := range []string{"X-Forwarded-For", "Forwarded", "Tailscale-User-Login"} {
		if r.Header.Get(h) != "" {
			return true
		}
	}
	return false
}

// openRemotely is what a device that is not paired yet may load: the page, and the pairing itself.
func openRemotely(r *http.Request) bool {
	switch r.Method + " " + r.URL.Path {
	case "GET /", "GET /manifest.webmanifest", "GET /icon.svg", "GET /icon-192.png", "GET /icon-512.png", "POST /api/pair":
		return true
	}
	return false
}

// localOnly is what no remote request reaches, paired or not: hooks, and the pairing of other devices.
func localOnly(r *http.Request) bool {
	p := r.URL.Path
	return p == "/api/hook" || p == "/api/status" || p == "/api/pair/code" || strings.HasPrefix(p, "/api/devices")
}

// refuseRemote checks a request from another machine. It returns a zero status when the request may go on.
func (s *Server) refuseRemote(w http.ResponseWriter, r *http.Request) (int, string) {
	// A page on any website can rename itself to 127.0.0.1 and add a proxy header (DNS rebinding):
	// only the tailnet's own names, and the configured host, are let in.
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if !strings.HasSuffix(host, ".ts.net") && host != s.cfg.Host {
		return http.StatusForbidden, "Unknown host"
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		if _, rest, found := strings.Cut(origin, "://"); !found || rest != r.Host {
			return http.StatusForbidden, "Requests from other origins are refused"
		}
	}
	if r.ContentLength != 0 && !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return http.StatusUnsupportedMediaType, "Send application/json"
	}
	if localOnly(r) {
		return http.StatusForbidden, "Only the computer running hc can do this"
	}
	if openRemotely(r) {
		return 0, ""
	}
	name := ""
	if c, err := r.Cookie(deviceCookie); err == nil && c.Value != "" {
		var err error
		if name, err = s.st.DeviceByToken(c.Value); err != nil {
			return http.StatusInternalServerError, "Internal error: " + err.Error()
		}
	}
	if name == "" {
		return http.StatusUnauthorized, "Pair this device: run hc pair on the computer, then enter the code"
	}
	s.seen(w, name, r.Cookie)
	return 0, ""
}

// seen records a paired device's visit, at most once a minute, and keeps its cookie from expiring.
func (s *Server) seen(w http.ResponseWriter, name string, cookie func(string) (*http.Cookie, error)) {
	s.pair.mu.Lock()
	if s.pair.seen == nil {
		s.pair.seen = map[string]time.Time{}
	}
	fresh := time.Since(s.pair.seen[name]) < seenEvery
	if !fresh {
		s.pair.seen[name] = time.Now()
	}
	s.pair.mu.Unlock()
	if fresh {
		return
	}
	_ = s.st.SeeDevice(name)
	if c, err := cookie(deviceCookie); err == nil {
		setDeviceCookie(w, c.Value)
	}
}

func setDeviceCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name: deviceCookie, Value: token, Path: "/", MaxAge: int(cookieAge.Seconds()),
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
	})
}

func randomCode() (string, error) {
	b := make([]byte, codeLength)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = codeAlphabet[int(b[i])%len(codeAlphabet)] // 256 is a multiple of 32: no bias
	}
	return string(b), nil
}

// pairCode hands out a code for one device. A new code replaces the one before.
func (s *Server) pairCode(r *http.Request) (any, error) {
	data, err := body(r)
	if err != nil {
		return nil, err
	}
	name, _ := data["name"].(string)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || utf8.RuneCountInString(name) > 40 || strings.ContainsAny(name, "/\n\t") {
		return nil, fail(400, "Name the device in 1 to 40 characters, without a slash")
	}
	code, err := randomCode()
	if err != nil {
		return nil, err
	}
	s.pair.mu.Lock()
	defer s.pair.mu.Unlock()
	s.pair.code, s.pair.name, s.pair.expires, s.pair.wrong = code, name, time.Now().Add(codeTTL), 0
	return map[string]any{"code": code[:4] + "-" + code[4:], "name": name, "expires_at": float64(s.pair.expires.Unix())}, nil
}

// pairDevice trades the code for a token the device keeps in its cookie. A code works once.
func (s *Server) pairDevice(w http.ResponseWriter, r *http.Request) {
	data, err := body(r)
	if err != nil {
		respond(w, nil, err)
		return
	}
	typed, _ := data["code"].(string)
	typed = strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(typed))
	s.pair.mu.Lock()
	switch {
	case s.pair.code == "" || time.Now().After(s.pair.expires):
		s.pair.code = ""
		s.pair.mu.Unlock()
		respond(w, nil, fail(403, "No code is waiting: run hc pair on the computer"))
		return
	case typed != s.pair.code:
		s.pair.wrong++
		if s.pair.wrong >= maxWrongCodes {
			s.pair.code = "" // guessing stops here: a new code needs the computer
		}
		s.pair.mu.Unlock()
		respond(w, nil, fail(403, "Wrong code"))
		return
	}
	name := s.pair.name
	s.pair.code = ""
	s.pair.mu.Unlock()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		respond(w, nil, err)
		return
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	if err := s.st.PairDevice(name, token); err != nil {
		respond(w, nil, err)
		return
	}
	setDeviceCookie(w, token)
	respond(w, map[string]string{"name": name}, nil)
}

func (s *Server) listDevices(*http.Request) (any, error) { return s.st.Devices() }

func (s *Server) forgetDevice(r *http.Request) (any, error) {
	name := r.PathValue("name")
	found, err := s.st.ForgetDevice(name)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fail(404, "No paired device named %s", name)
	}
	return map[string]string{"name": name}, nil
}
