package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// Device hints are descriptive; addresses come from the authenticated socket
// peer and cannot be replaced by client-supplied proxy headers.
func TestSessionDevicesAndObservedIP(t *testing.T) {
	c := newAuthClient(t)
	c.do("POST", "/api/auth/setup", `{"username":"owner","password":"password123"}`)
	r := httptest.NewRequest("GET", "/api/auth/sessions", nil)
	r.RemoteAddr = "[2001:db8::7]:9000"
	r.Header.Set("X-Forwarded-For", "203.0.113.99")
	r.AddCookie(c.cookie)
	w := httptest.NewRecorder()
	c.handler.ServeHTTP(w, r)
	var sessions []sessionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &sessions); err != nil || len(sessions) != 1 {
		t.Fatalf("session response: %s (%v)", w.Body.String(), err)
	}
	if sessions[0].IPAddress != "2001:db8::7" || !sessions[0].Current {
		t.Fatalf("unexpected address/current session: %+v", sessions[0])
	}
	for _, tc := range []struct{ agent, name, kind string }{
		{"AlreadyOn-Android/TV", "Google TV", "TV"},
		{"AlreadyOn-Android/Mobile", "Android Phone", "Phone"},
		{"Mozilla Android Mobile Chrome/140", "Android Phone", "Web Browser"},
		{"Mozilla Android Chrome/140", "Android Phone", "Web Browser"},
		{"Mozilla Windows Edg/140 Chrome/140", "Windows", "Web Browser"},
		{"Mozilla Mac OS X Safari/140", "Mac", "Web Browser"},
		{"Mozilla Linux Firefox/140", "Linux", "Web Browser"},
		{"", "Unknown Device", "Web Browser"},
	} {
		name, kind := sessionDevice(tc.agent)
		if name != tc.name || kind != tc.kind {
			t.Errorf("%q: %q/%q", tc.agent, name, kind)
		}
	}
}
