package server

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// fromPhone sends a request the way tailscale serve forwards one from another machine.
func (f *fixture) fromPhone(method, path string, body any, headers ...string) response {
	f.t.Helper()
	return f.do(method, path, body, append([]string{"Host", "board.example.ts.net", "X-Forwarded-For", "100.64.0.9"}, headers...)...)
}

// pairPhone pairs a device and returns its cookie.
func (f *fixture) pairPhone(name string) string {
	f.t.Helper()
	code := f.post("/api/pair/code", obj{"name": name})
	if code.status != 200 {
		f.t.Fatalf("code: %d %s", code.status, code.raw)
	}
	res := f.fromPhone("POST", "/api/pair", obj{"code": strings.ToLower(code.str("code"))})
	if res.status != 200 {
		f.t.Fatalf("pair: %d %s", res.status, res.raw)
	}
	cookie := res.header.Get("Set-Cookie")
	for _, want := range []string{"hc_device=", "HttpOnly", "Secure", "SameSite=Strict", "Max-Age="} {
		if !strings.Contains(cookie, want) {
			f.t.Errorf("cookie %q lacks %s", cookie, want)
		}
	}
	return strings.Split(cookie, ";")[0]
}

func TestRemoteNeedsAPairedDevice(t *testing.T) {
	f := newFixture(t)

	// Nothing of the board without pairing, whatever Host the client claims.
	for _, extra := range [][]string{nil, {"Host", "127.0.0.1"}, {"Host", "localhost:7337"}} {
		if res := f.fromPhone("GET", "/api/tasks", nil, extra...); res.status != http.StatusUnauthorized && res.status != http.StatusForbidden {
			t.Errorf("%v: %d", extra, res.status)
		}
	}
	if res := f.do("GET", "/api/tasks", nil, "Host", "board.example.ts.net", "Tailscale-User-Login", "someone@example.com"); res.status != http.StatusUnauthorized {
		t.Errorf("tailscale header alone: %d", res.status)
	}
	// Straight to the port, when host is not loopback: no proxy header, a local Host, still remote.
	f.cfg.Host = "100.64.0.1"
	if res := f.do("GET", "/api/tasks", nil, "Host", "127.0.0.1", "RemoteAddr", "100.64.0.9:41000"); res.status != http.StatusForbidden {
		t.Errorf("direct with a local Host: %d", res.status)
	}
	if res := f.do("GET", "/api/tasks", nil, "Host", "100.64.0.1:7337", "RemoteAddr", "100.64.0.9:41000"); res.status != http.StatusUnauthorized {
		t.Errorf("direct: %d", res.status)
	}
	f.cfg.Host = "127.0.0.1"
	// A rebound website: its own name, a proxy header it added itself.
	if res := f.do("POST", "/api/pair", obj{"code": "x"}, "Host", "evil.example:7337", "X-Forwarded-For", "1.2.3.4", "Origin", "http://evil.example:7337"); res.status != http.StatusForbidden || !strings.Contains(res.str("error"), "Unknown host") {
		t.Errorf("rebinding: %d %s", res.status, res.raw)
	}
	// The page and what installs it load, so the page can ask for the code.
	for _, path := range []string{"/", "/manifest.webmanifest", "/icon.svg", "/icon-192.png", "/icon-512.png"} {
		if res := f.fromPhone("GET", path, nil); res.status != 200 {
			t.Errorf("%s: %d", path, res.status)
		}
	}
	// Hooks and the pairing of devices stay on the computer, even for a paired device.
	cookie := f.pairPhone("phone")
	for _, r := range [][2]string{{"POST", "/api/hook"}, {"POST", "/api/status"}, {"POST", "/api/pair/code"}, {"GET", "/api/devices"}, {"DELETE", "/api/devices/phone"}} {
		if res := f.fromPhone(r[0], r[1], obj{"name": "x"}, "Cookie", cookie); res.status != http.StatusForbidden {
			t.Errorf("%s %s: %d", r[0], r[1], res.status)
		}
	}
	// A paired device reaches the board; another origin still does not.
	if res := f.fromPhone("GET", "/api/tasks", nil, "Cookie", cookie); res.status != 200 {
		t.Errorf("paired: %d %s", res.status, res.raw)
	}
	if res := f.fromPhone("GET", "/api/tasks", nil, "Cookie", cookie, "Origin", "https://evil.example"); res.status != http.StatusForbidden {
		t.Errorf("other origin: %d", res.status)
	}
	if res := f.fromPhone("GET", "/api/tasks", nil, "Cookie", "hc_device=forged"); res.status != http.StatusUnauthorized {
		t.Errorf("forged token: %d", res.status)
	}

	// Unpaired from the computer, the device is out.
	devices := f.get("/api/devices")
	if !strings.Contains(string(devices.raw), `"name":"phone"`) || strings.Contains(string(devices.raw), "hc_device") {
		t.Errorf("devices: %s", devices.raw)
	}
	if res := f.do("DELETE", "/api/devices/phone", nil); res.status != 200 {
		t.Errorf("rm: %d", res.status)
	}
	if res := f.fromPhone("GET", "/api/tasks", nil, "Cookie", cookie); res.status != http.StatusUnauthorized {
		t.Errorf("after rm: %d", res.status)
	}
	if res := f.do("DELETE", "/api/devices/phone", nil); res.status != 404 {
		t.Errorf("rm twice: %d", res.status)
	}
}

func TestPairCodes(t *testing.T) {
	f := newFixture(t)
	for _, name := range []string{" ", ".", "..", "a/b", strings.Repeat("x", 41)} {
		if res := f.post("/api/pair/code", obj{"name": name}); res.status != 400 {
			t.Errorf("name %q: %d", name, res.status)
		}
	}
	if res := f.fromPhone("POST", "/api/pair", obj{"code": "AAAA-AAAA"}); res.status != 403 || !strings.Contains(res.str("error"), "No code is waiting") {
		t.Errorf("no code: %d %s", res.status, res.raw)
	}

	// A code works once.
	code := f.post("/api/pair/code", obj{"name": "phone"}).str("code")
	if len(code) != 9 || code[4] != '-' {
		t.Fatalf("code %q", code)
	}
	if res := f.fromPhone("POST", "/api/pair", obj{"code": code}); res.status != 200 {
		t.Fatalf("pair: %d", res.status)
	}
	if res := f.fromPhone("POST", "/api/pair", obj{"code": code}); res.status != 403 {
		t.Errorf("reused: %d", res.status)
	}

	// Five wrong codes and the right one no longer works.
	code = f.post("/api/pair/code", obj{"name": "tablet"}).str("code")
	for i := 0; i < maxWrongCodes; i++ {
		if res := f.fromPhone("POST", "/api/pair", obj{"code": "2222-2222"}); res.status != 403 {
			t.Errorf("wrong %d: %d", i, res.status)
		}
	}
	if res := f.fromPhone("POST", "/api/pair", obj{"code": code}); res.status != 403 {
		t.Errorf("after guessing: %d", res.status)
	}

	// A name is counted in characters, not bytes.
	if res := f.post("/api/pair/code", obj{"name": strings.Repeat("📱", 20)}); res.status != 200 {
		t.Errorf("multibyte name: %d %s", res.status, res.raw)
	}

	// An expired code does not work.
	code = f.post("/api/pair/code", obj{"name": "tablet"}).str("code")
	f.srv.pair.expires = time.Now().Add(-time.Second)
	if res := f.fromPhone("POST", "/api/pair", obj{"code": code}); res.status != 403 {
		t.Errorf("expired: %d", res.status)
	}
}

func TestRandomCode(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		c, err := randomCode()
		if err != nil || len(c) != codeLength || strings.Trim(c, codeAlphabet) != "" || seen[c] {
			t.Fatalf("code %q, %v", c, err)
		}
		seen[c] = true
	}
}
