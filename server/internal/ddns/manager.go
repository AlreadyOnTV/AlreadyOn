package ddns

import (
	"context"
	"database/sql"
	"errors"
	"github.com/alreadyonapp/alreadyon/server/internal/logging"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Manager serializes provider updates and configuration changes so an in-flight
// update never publishes status under a different provider's credentials.
type Manager struct {
	db      *sql.DB
	client  *http.Client
	mu      sync.Mutex
	running atomic.Value
	now     func() time.Time
}

func New(db *sql.DB) *Manager {
	m := &Manager{db: db, now: time.Now, client: &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	m.running.Store("")
	return m
}

// Run explicitly tests a saved provider, even with automatic updates disabled.
// It performs no credential discovery, router configuration or certificate work.
func (m *Manager) Run(ctx context.Context, provider string) (Status, error) {
	return m.run(ctx, provider, true)
}
func (m *Manager) run(ctx context.Context, provider string, manual bool) (Status, error) {
	return m.runScheduled(ctx, provider, manual, false)
}

// Startup can reverify an accepted update despite verification-only backoff.
// Provider refusals, rate limits and blocks retain their delays across restart.
func startupEligible(c Config) bool {
	return !c.Blocked && (c.LastError == "" || (c.LastUpdate > 0 && strings.HasPrefix(c.LastError, "DNS update accepted")))
}
func (m *Manager) runScheduled(ctx context.Context, provider string, manual, startup bool) (Status, error) {
	if provider != "cloudflare" && provider != "no-ip" {
		return Status{}, ErrInvalid
	}
	if !m.mu.TryLock() {
		return Status{}, ErrBusy
	}
	defer m.mu.Unlock()
	c, err := load(ctx, m.db, provider)
	if err != nil {
		return Status{}, err
	}
	if c.Hostname == "" || c.Secret == "" || (provider == "no-ip" && c.Username == "") {
		return Status{}, ErrInvalid
	}
	now := m.now().Unix()
	if c.Blocked || (now < c.NextAttempt && !(startup && startupEligible(c))) {
		logging.Info("DDNS %s: sync skipped; blocked=%t; next retry=%s", provider, c.Blocked, time.Unix(c.NextAttempt, 0).UTC().Format(time.RFC3339))
		return m.Status(ctx)
	}
	if !manual {
		var active string
		if err = m.db.QueryRowContext(ctx, `SELECT ddns_provider FROM server_settings WHERE id=1`).Scan(&active); err != nil {
			return Status{}, err
		}
		if active != provider || !c.Enabled {
			return m.Status(ctx)
		}
	}
	m.running.Store(provider)
	defer m.running.Store("")
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	if _, err = m.db.ExecContext(ctx, `UPDATE ddns_configs SET last_attempt=? WHERE provider=?`, now, provider); err != nil {
		return Status{}, err
	}
	logging.Info("DDNS %s: sync started; startup=%t; attempt=%s", provider, startup, time.Unix(now, 0).UTC().Format(time.RFC3339))
	ip, opErr := m.publicIP(ctx)
	if opErr == nil && (manual || startup || c.Provider == "cloudflare" || ip != c.LastIP || c.LastUpdate == 0) {
		opErr = m.update(ctx, c, ip)
		if opErr == nil {
			c.LastUpdate = now
			c.LastIP = ip
		}
	}
	if opErr == nil {
		opErr = m.verify(ctx, c.Hostname, ip)
		if opErr == nil {
			c.LastVerified = now
		}
	}
	message := ""
	blocked := false
	failures := 0
	delay := 5 * time.Minute
	if opErr != nil {
		message = opErr.Error()
		failures = c.Failures + 1
		var pe *providerError
		blocked = errors.As(opErr, &pe) && pe.permanent
		delay = 5 * time.Minute
		for i := 1; i < failures && delay < time.Hour; i++ {
			delay *= 2
		}
		delay = min(delay, time.Hour)
		if pe != nil {
			delay = max(delay, pe.retryAfter)
		}
		// Provider outages must not trigger another update every few minutes.
		if message == "provider temporarily unavailable; automatic updates will retry with backoff" {
			delay = max(delay, 30*time.Minute)
		}
	}
	// Use a short independent write context to record timeouts and cancellations;
	// never return raw transport/provider errors containing credentials.
	record, cancelRecord := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelRecord()
	_, err = m.db.ExecContext(record, `UPDATE ddns_configs SET last_update=?,last_verified=?,last_ip=?,last_error=?,failures=?,next_attempt=?,blocked=? WHERE provider=?`, c.LastUpdate, c.LastVerified, c.LastIP, message, failures, now+int64(delay/time.Second), blocked, provider)
	if err != nil {
		return Status{}, err
	}
	if message == "" {
		logging.Info("DDNS %s: sync succeeded; public DNS verified; attempt=%s", provider, time.Unix(now, 0).UTC().Format(time.RFC3339))
	} else {
		logging.Warn("DDNS %s: sync failed; attempt=%s; %s", provider, time.Unix(now, 0).UTC().Format(time.RFC3339), message)
	}
	m.running.Store("")
	return m.Status(record)
}

// Start waits one minute before the initial sync to avoid competing with
// startup work, then checks the persisted normal cadence/backoff every minute.
// Disabled/unconfigured instances make no requests and log nothing while the
// provider is None; cancellation ends the delay.
func (m *Manager) Start(ctx context.Context) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	if status, err := m.Status(ctx); err == nil && status.Provider != "none" {
		logging.Info("DDNS: startup sync scheduled in one minute")
	}
	startup := true
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		status, err := m.Status(ctx)
		if err != nil {
			logging.Warn("DDNS: scheduled check could not read settings")
		} else if status.Provider != "none" {
			// Provider None is the unconfigured default: no check and no log line.
			for _, c := range status.Configs {
				if c.Provider != status.Provider {
					continue
				}
				logging.Info("DDNS %s: scheduled check; startup=%t", c.Provider, startup)
				if !c.Enabled {
					logging.Info("DDNS %s: sync skipped; configuration incomplete or disabled", c.Provider)
					continue
				}
				if c.Blocked {
					logging.Info("DDNS %s: sync skipped; provider blocked; correct setup and save", c.Provider)
					continue
				}
				if m.now().Unix() < c.NextAttempt && !(startup && startupEligible(c)) {
					logging.Info("DDNS %s: sync waiting until %s", c.Provider, time.Unix(c.NextAttempt, 0).UTC().Format(time.RFC3339))
					continue
				}
				if _, err := m.runScheduled(ctx, c.Provider, false, startup); err != nil {
					logging.Warn("DDNS %s: sync could not run; configuration unavailable or another operation is active", c.Provider)
				}
			}
		}
		startup = false
	}
}
