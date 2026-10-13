package ddns

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/alreadyonapp/alreadyon/server/internal/logging"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// providerError contains reviewed messages only, never response bodies,
// transport errors or secret-bearing provider requests.
type providerError struct {
	message    string
	permanent  bool
	retryAfter time.Duration
}

func (e *providerError) Error() string { return e.message }
func permanent(message string) error   { return &providerError{message: message, permanent: true} }

func (m *Manager) request(ctx context.Context, method, endpoint string, c *Config, payload any) ([]byte, error) {
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, errors.New("could not prepare DNS update")
		}
		body = bytes.NewReader(b)
	}
	r, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, errors.New("could not prepare provider request")
	}
	r.Header.Set("User-Agent", "AlreadyOn DDNS/1.0 (+https://github.com/AlreadyOnTV/AlreadyOn)")
	// Cloudflare's JSON DoH endpoint requires this exact media type; a
	// comma-separated Accept list is rejected with HTTP 400.
	if r.URL.Host == "cloudflare-dns.com" {
		r.Header.Set("Accept", "application/dns-json")
	} else {
		r.Header.Set("Accept", "application/json, text/plain")
	}
	if payload != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if c != nil {
		if c.Provider == "cloudflare" {
			r.Header.Set("Authorization", "Bearer "+c.Secret)
		} else if c.Provider == "no-ip" {
			r.SetBasicAuth(c.Username, c.Secret)
		}
	}
	resp, err := m.client.Do(r)
	if err != nil {
		return nil, errors.New("provider connection failed or timed out; check internet access")
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return nil, permanent("provider refused credentials or permissions; correct setup and save again")
	}
	if resp.StatusCode == 429 {
		delay := 5 * time.Minute
		if seconds, err := strconv.ParseInt(resp.Header.Get("Retry-After"), 10, 64); err == nil && seconds > 0 && seconds <= 86400 {
			delay = max(delay, time.Duration(seconds)*time.Second)
		} else if until, err := http.ParseTime(resp.Header.Get("Retry-After")); err == nil {
			delay = max(delay, until.Sub(m.now()))
		}
		return nil, &providerError{message: "provider rate limited updates; waiting before retry", retryAfter: min(delay, 24*time.Hour)}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("provider returned HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 128*1024+1))
	if err != nil || len(b) > 128*1024 {
		return nil, errors.New("provider response is unreadable or too large")
	}
	return b, nil
}

func (m *Manager) publicIP(ctx context.Context) (string, error) {
	b, err := m.request(ctx, "GET", "https://api.ipify.org", nil, nil)
	if err != nil {
		return "", errors.New("public IPv4 detection failed; check internet access")
	}
	ip, err := netip.ParseAddr(strings.TrimSpace(string(b)))
	if err != nil || !ip.Is4() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || netip.MustParsePrefix("100.64.0.0/10").Contains(ip) {
		return "", errors.New("address detection did not return a public IPv4 address")
	}
	return ip.String(), nil
}

func (m *Manager) verify(ctx context.Context, hostname, ip string) error {
	b, err := m.request(ctx, "GET", "https://cloudflare-dns.com/dns-query?"+url.Values{"name": {hostname}, "type": {"A"}}.Encode(), nil, nil)
	if err != nil {
		return errors.New("DNS update accepted, but public DNS verification could not complete")
	}
	var v struct {
		Status int
		Answer []struct {
			Type int
			Data string
		}
	}
	if json.Unmarshal(b, &v) != nil || v.Status != 0 {
		return errors.New("DNS update accepted; public DNS is not resolving yet")
	}
	for _, a := range v.Answer {
		if a.Type == 1 && a.Data == ip {
			return nil
		}
	}
	return errors.New("DNS update accepted; public DNS does not yet match the detected IPv4 address")
}

func (m *Manager) update(ctx context.Context, c Config, ip string) error {
	if c.Provider == "cloudflare" {
		return m.updateCloudflare(ctx, c, ip)
	}
	if c.Provider != "no-ip" {
		return ErrInvalid
	}
	q := url.Values{"hostname": {c.Hostname}, "myip": {ip}}
	endpoint := "https://dynupdate.no-ip.com/nic/update"
	b, err := m.request(ctx, "GET", endpoint+"?"+q.Encode(), &c, nil)
	if err != nil {
		return err
	}
	fields := strings.Fields(string(b))
	if len(fields) == 0 {
		return errors.New("provider returned an empty update result")
	}
	code := fields[0]
	switch code {
	case "good", "nochg":
		if len(fields) != 2 || fields[1] != ip {
			return errors.New("provider returned an unexpected update address")
		}
		return nil
	case "badauth":
		return permanent("provider refused the username/password; correct credentials and save again")
	case "nohost", "notfqdn":
		return permanent("provider could not find this hostname in the account; correct setup and save again")
	case "abuse", "badagent", "!donator":
		return permanent("provider blocked the update client or requires a different plan; check provider support before saving again")
	case "911", "dnserr":
		return errors.New("provider temporarily unavailable; automatic updates will retry with backoff")
	default:
		return permanent("provider rejected the update; check account/hostname settings before saving again")
	}
}

// Create or update one A record and enforce DNS-only routing. PATCH preserves
// existing TTL/comment metadata; already-correct records need no write.
func (m *Manager) updateCloudflare(ctx context.Context, c Config, ip string) error {
	if c.ZoneID == "" {
		zone, err := m.discoverZone(ctx, c)
		if err != nil {
			return err
		}
		c.ZoneID = zone
		if _, err = m.db.ExecContext(ctx, `UPDATE ddns_configs SET zone_id=? WHERE provider='cloudflare'`, zone); err != nil {
			return errors.New("could not store discovered Cloudflare zone")
		}
	}
	base := "https://api.cloudflare.com/client/v4/zones/" + c.ZoneID + "/dns_records"
	b, err := m.request(ctx, "GET", base+"?"+url.Values{"type": {"A"}, "name": {c.Hostname}, "per_page": {"2"}}.Encode(), &c, nil)
	if err != nil {
		return err
	}
	var records struct {
		Success bool
		Result  []struct {
			ID      string
			Name    string
			Type    string
			Content string
			Proxied bool
		}
	}
	if json.Unmarshal(b, &records) != nil || !records.Success {
		return permanent("Cloudflare could not read the DNS record; check zone ID and token permissions")
	}
	if len(records.Result) > 1 {
		return permanent("Cloudflare has multiple A records for this hostname; keep one record before syncing")
	}
	method, endpoint := "POST", base
	payload := map[string]any{"type": "A", "name": c.Hostname, "content": ip, "proxied": false, "ttl": 1}
	if len(records.Result) == 1 {
		r := records.Result[0]
		if !zonePattern.MatchString(r.ID) || !strings.EqualFold(r.Name, c.Hostname) || r.Type != "A" {
			return permanent("Cloudflare returned a record that does not match this hostname")
		}
		if r.Content == ip && !r.Proxied {
			logging.Info("DDNS cloudflare: A record already correct and DNS only; no write needed")
			return nil
		}
		method, endpoint = "PATCH", base+"/"+r.ID
		payload = map[string]any{"content": ip, "proxied": false}
	}
	logging.Info("DDNS cloudflare: %s A record with DNS-only routing", map[string]string{"POST": "creating", "PATCH": "updating"}[method])
	b, err = m.request(ctx, method, endpoint, &c, payload)
	if err != nil {
		return err
	}
	var result struct {
		Success bool
		Result  struct {
			Content string
			Proxied bool
		}
	}
	if json.Unmarshal(b, &result) != nil || !result.Success || result.Result.Content != ip || result.Result.Proxied {
		return errors.New("Cloudflare did not confirm the requested address update")
	}
	return nil
}

// Discover only zones visible to the scoped token. Choose the longest matching
// DNS suffix, including delegated subdomain zones, and bound pagination.
func (m *Manager) discoverZone(ctx context.Context, c Config) (string, error) {
	bestID, bestName := "", ""
	for page := 1; page <= 10; page++ {
		b, err := m.request(ctx, "GET", "https://api.cloudflare.com/client/v4/zones?"+url.Values{"status": {"active"}, "per_page": {"50"}, "page": {strconv.Itoa(page)}}.Encode(), &c, nil)
		if err != nil {
			return "", err
		}
		var v struct {
			Success    bool
			Result     []struct{ ID, Name string }
			ResultInfo struct {
				TotalPages int `json:"total_pages"`
			} `json:"result_info"`
		}
		if json.Unmarshal(b, &v) != nil || !v.Success {
			return "", permanent("Cloudflare zone discovery failed; grant Zone → Zone → Read and Zone → DNS → Edit for this domain")
		}
		for _, z := range v.Result {
			name := strings.ToLower(z.Name)
			if !zonePattern.MatchString(z.ID) || !hostPattern.MatchString(name) {
				continue
			}
			if c.Hostname != name && !strings.HasSuffix(c.Hostname, "."+name) {
				continue
			}
			if len(name) > len(bestName) {
				bestName, bestID = name, z.ID
			} else if name == bestName && bestID != z.ID {
				return "", permanent("Cloudflare zone discovery is ambiguous; restrict the token to this domain")
			}
		}
		if v.ResultInfo.TotalPages <= page {
			if bestID == "" {
				return "", permanent("no active Cloudflare zone matches this hostname; check hostname and token Zone Read permission")
			}
			return bestID, nil
		}
	}
	return "", permanent("too many Cloudflare zones to discover safely; restrict the token to this domain")
}
