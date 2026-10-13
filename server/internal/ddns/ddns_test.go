package ddns

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/storage"
)

// Provider fixtures never contact public DNS, alter real records or reuse a
// preview database. The transport also inspects credential placement/redaction.
type fixtureTransport func(*http.Request) (*http.Response, error)

func (f fixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(body string) *http.Response {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}
func testManager(t *testing.T) *Manager {
	t.Helper()
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return New(db.DB)
}
func TestProviderSecretIsolationAndCustomRemoval(t *testing.T) {
	m := testManager(t)
	ctx := context.Background()
	secret := "synthetic-noip-password"
	if _, err := m.Save(ctx, Update{Provider: "no-ip", Username: "fixture", Hostname: "isolated.example.com", Secret: &secret}); err != nil {
		t.Fatal(err)
	}
	s, err := m.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(s)
	if strings.Contains(string(b), secret) || strings.Contains(string(b), `"secret"`) {
		t.Fatal("credential disclosed")
	}
	for _, c := range s.Configs {
		if c.Provider != "no-ip" && c.HasSecret {
			t.Fatal("credential crossed provider boundary")
		}
	}
	for _, retired := range []string{"custom", "dyndns", "duckdns"} {
		if _, err := m.Save(ctx, Update{Provider: retired}); err != ErrInvalid {
			t.Fatalf("retired provider accepted: %v", err)
		}
		if _, err := m.Run(ctx, retired); err != ErrInvalid {
			t.Fatal("retired provider test accepted")
		}
	}
	if _, err := m.Save(ctx, Update{Provider: "no-ip", Username: "fixture", Hostname: "isolated.example.com", ClearSecret: true, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	c, _ := load(ctx, m.db, "no-ip")
	if c.HasSecret || c.Enabled {
		t.Fatal("clear did not disable updates")
	}
}
func TestAllProviderUpdatesAndVerification(t *testing.T) {
	for _, provider := range Providers[1:] {
		t.Run(provider, func(t *testing.T) {
			m := testManager(t)
			secret := "synthetic-secret"
			ctx := context.Background()
			hostname := "tv.example.com"
			if _, err := m.Save(ctx, Update{Provider: provider, Hostname: hostname, Username: "fixture", ZoneID: strings.Repeat("a", 32), Secret: &secret}); err != nil {
				t.Fatal(err)
			}
			updates := 0
			m.client.Transport = fixtureTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host == "api.ipify.org" {
					return response("203.0.113.10"), nil
				}
				if r.URL.Host == "cloudflare-dns.com" {
					if r.Header.Get("Accept") != "application/dns-json" {
						t.Fatal("public DNS verification requires the exact JSON Accept header")
					}
					return response(`{"Status":0,"Answer":[{"type":1,"data":"203.0.113.10"}]}`), nil
				}
				if provider == "cloudflare" && r.URL.Path == "/client/v4/zones" {
					return response(`{"success":true,"result":[{"id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","name":"example.com"}],"result_info":{"total_pages":1}}`), nil
				}
				updates++
				if provider == "cloudflare" {
					if r.Header.Get("Authorization") != "Bearer "+secret {
						t.Fatal("missing scoped credential")
					}
					if r.Method == "GET" {
						return response(`{"success":true,"result":[{"id":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","name":"tv.example.com","type":"A","content":"203.0.113.9","proxied":false}]}`), nil
					}
					if r.Method != "PATCH" {
						t.Fatal("unexpected mutation")
					}
					return response(`{"success":true,"result":{"content":"203.0.113.10"}}`), nil
				}
				user, pass, ok := r.BasicAuth()
				if !ok || user != "fixture" || pass != secret || r.URL.User != nil {
					t.Fatal("bad update authentication")
				}
				if provider == "no-ip" && r.URL.Host != "dynupdate.no-ip.com" {
					t.Fatal("wrong No-IP endpoint")
				}
				return response("good 203.0.113.10"), nil
			})
			s, err := m.Run(ctx, provider)
			if err != nil {
				t.Fatal(err)
			}
			c, _ := load(ctx, m.db, provider)
			if c.LastUpdate == 0 || c.LastVerified == 0 || c.LastError != "" || s.Running != "" {
				t.Fatalf("update/verification incomplete: %+v", s)
			}
			before := updates
			if _, err := m.Run(ctx, provider); err != nil {
				t.Fatal(err)
			}
			if updates != before {
				t.Fatal("manual test bypassed cooldown")
			}
		})
	}
}
func TestProviderRejectionStopsRetryAndRedactsBody(t *testing.T) {
	m := testManager(t)
	secret := "synthetic-sensitive"
	ctx := context.Background()
	_, err := m.Save(ctx, Update{Provider: "no-ip", Username: "fixture", Hostname: "isolated.example.com", Secret: &secret, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	m.client.Transport = fixtureTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "api.ipify.org" {
			return response("203.0.113.10"), nil
		}
		return response("KO " + secret), nil
	})
	_, err = m.Run(ctx, "no-ip")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := load(ctx, m.db, "no-ip")
	if !c.Blocked || strings.Contains(c.LastError, secret) || c.LastUpdate != 0 {
		t.Fatal("unsafe rejection handling")
	}
}
func TestUnchangedAddressSkipsMutationAndRestartKeepsBackoff(t *testing.T) {
	m := testManager(t)
	ctx := context.Background()
	secret := "fixture"
	now := time.Now()
	m.now = func() time.Time { return now }
	_, err := m.Save(ctx, Update{Provider: "no-ip", Username: "fixture", Hostname: "isolated.example.com", Secret: &secret, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	updates := 0
	m.client.Transport = fixtureTransport(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host {
		case "api.ipify.org":
			return response("203.0.113.10"), nil
		case "cloudflare-dns.com":
			return response(`{"Status":0,"Answer":[{"type":1,"data":"203.0.113.10"}]}`), nil
		default:
			updates++
			return response("good 203.0.113.10"), nil
		}
	})
	if _, err = m.run(ctx, "no-ip", false); err != nil {
		t.Fatal(err)
	}
	now = now.Add(6 * time.Minute)
	if _, err = m.run(ctx, "no-ip", false); err != nil {
		t.Fatal(err)
	}
	if updates != 1 {
		t.Fatal("unchanged address mutated DNS")
	}
	c, _ := load(ctx, m.db, "no-ip")
	fresh := New(m.db)
	s, err := fresh.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range s.Configs {
		if v.Provider == c.Provider && v.NextAttempt != c.NextAttempt {
			t.Fatal("restart lost pacing")
		}
	}
}

// Cloudflare must create missing records, correct proxy routing even when the
// address matches, and avoid writes for an already-correct DNS-only record.
func TestCloudflareRecordReconciliation(t *testing.T) {
	for _, scenario := range []struct{ name, records, method string }{
		{"missing", `[]`, "POST"},
		{"proxied", `[{"id":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","name":"tv.example.com","type":"A","content":"203.0.113.10","proxied":true}]`, "PATCH"},
		{"correct", `[{"id":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","name":"tv.example.com","type":"A","content":"203.0.113.10","proxied":false}]`, ""},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			m := testManager(t)
			mutations := 0
			m.client.Transport = fixtureTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method == "GET" {
					return response(`{"success":true,"result":` + scenario.records + `}`), nil
				}
				mutations++
				if r.Method != scenario.method {
					t.Fatalf("unexpected method %s", r.Method)
				}
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Fatal(err)
				}
				if payload["proxied"] != false || payload["content"] != "203.0.113.10" {
					t.Fatal("incorrect address/proxy payload")
				}
				if r.Method == "POST" && (payload["type"] != "A" || payload["name"] != "tv.example.com") {
					t.Fatal("incorrect create payload")
				}
				return response(`{"success":true,"result":{"content":"203.0.113.10","proxied":false}}`), nil
			})
			err := m.updateCloudflare(context.Background(), Config{Provider: "cloudflare", Hostname: "tv.example.com", ZoneID: strings.Repeat("a", 32), Secret: "fixture"}, "203.0.113.10")
			if err != nil {
				t.Fatal(err)
			}
			if (scenario.method == "" && mutations != 0) || (scenario.method != "" && mutations != 1) {
				t.Fatalf("unexpected writes %d", mutations)
			}
		})
	}
}

// Restart must refresh a healthy configuration despite its normal cooldown,
// while preserving failure backoff rather than hammering a refusing provider.
func TestStartupSyncPreservesFailureBackoff(t *testing.T) {
	m := testManager(t)
	ctx := context.Background()
	secret := "fixture"
	if _, err := m.Save(ctx, Update{Provider: "no-ip", Username: "fixture", Hostname: "isolated.example.com", Secret: &secret}); err != nil {
		t.Fatal(err)
	}
	future := m.now().Add(time.Hour).Unix()
	if _, err := m.db.Exec(`UPDATE ddns_configs SET next_attempt=? WHERE provider='no-ip'`, future); err != nil {
		t.Fatal(err)
	}
	writes := 0
	m.client.Transport = fixtureTransport(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host {
		case "api.ipify.org":
			return response("203.0.113.10"), nil
		case "cloudflare-dns.com":
			return response(`{"Status":0,"Answer":[{"type":1,"data":"203.0.113.10"}]}`), nil
		default:
			writes++
			return response("good 203.0.113.10"), nil
		}
	})
	if _, err := m.runScheduled(ctx, "no-ip", false, true); err != nil {
		t.Fatal(err)
	}
	if writes != 1 {
		t.Fatal("startup did not refresh healthy setup")
	}
	if _, err := m.db.Exec(`UPDATE ddns_configs SET next_attempt=?,last_error='provider rate limited updates; waiting before retry' WHERE provider='no-ip'`, future); err != nil {
		t.Fatal(err)
	}
	if _, err := m.runScheduled(ctx, "no-ip", false, true); err != nil {
		t.Fatal(err)
	}
	if writes != 1 {
		t.Fatal("startup bypassed persisted failure backoff")
	}
}

// A DNS verification failure follows an accepted update and is safe to
// recheck at startup; provider-side failures must keep their retry delay.
func TestStartupEligibilityDistinguishesVerificationFailure(t *testing.T) {
	if !startupEligible(Config{LastUpdate: 1, LastError: "DNS update accepted, but public DNS verification could not complete"}) {
		t.Fatal("accepted update cannot be reverified")
	}
	for _, c := range []Config{{LastError: "provider rate limited updates; waiting before retry"}, {LastError: "provider connection failed or timed out; check internet access"}, {Blocked: true}, {LastError: "DNS update accepted, but public DNS verification could not complete"}} {
		if startupEligible(c) {
			t.Fatal("startup bypassed provider failure or missing acceptance")
		}
	}
}
