// Package serversettings stores admin runtime choices. DDNS selection is kept
// here for compatibility, while provider credentials/status live in ddns_configs.
package serversettings

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var (
	Transcoding   = []string{"software", "intel", "nvidia-nvenc", "amd-vaapi"}
	TLSModes      = []string{"self-signed", "user-provided", "acme"}
	ACMEProviders = []string{"letsencrypt"}
	DDNSProviders = []string{"none", "cloudflare", "no-ip"}
)

var (
	ErrInvalidServerName  = errors.New("serverName must be 1–64 characters with no control characters")
	ErrInvalidTranscoding = errors.New("transcoding must be one of: software, intel, nvidia-nvenc, amd-vaapi")
	ErrInvalidTLSMode     = errors.New("tlsMode must be one of: self-signed, user-provided, acme")
	ErrInvalidACME        = errors.New("acmeProvider must be letsencrypt")
	ErrInvalidDomain      = errors.New("acmeDomain must be empty or a valid host name")
	ErrInvalidDDNS        = errors.New("ddnsProvider must be one of: none, cloudflare, no-ip")
	ErrDecodeNeedsEncode  = errors.New("hardwareDecoding needs a hardware encoder: choose one under transcoding first")
)

// domainPattern accepts dot-separated DNS labels (letters, digits, inner
// hyphens), 253 characters at most.
var domainPattern = regexp.MustCompile(`^([A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.)*[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

type Settings struct {
	ServerName   string
	Transcoding  string
	TLSMode      string
	ACMEProvider string
	ACMEDomain   string
	DDNSProvider string
	// HardwareDecoding is only ever true alongside a hardware Transcoding choice.
	HardwareDecoding bool
}

// Update lists the fields to change; nil fields are left alone.
type Update struct {
	ServerName       *string
	Transcoding      *string
	TLSMode          *string
	ACMEProvider     *string
	ACMEDomain       *string
	DDNSProvider     *string
	HardwareDecoding *bool
}

func Get(ctx context.Context, db *sql.DB) (Settings, error) {
	var s Settings
	err := db.QueryRowContext(ctx, `SELECT server_name, transcoding, tls_mode, acme_provider, acme_domain, ddns_provider, hardware_decoding FROM server_settings WHERE id = 1`).
		Scan(&s.ServerName, &s.Transcoding, &s.TLSMode, &s.ACMEProvider, &s.ACMEDomain, &s.DDNSProvider, &s.HardwareDecoding)
	return s, err
}

// Save validates and applies u, returning the resulting settings.
func Save(ctx context.Context, db *sql.DB, u Update) (Settings, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Settings{}, err
	}
	defer tx.Rollback()

	var s Settings
	if err := tx.QueryRowContext(ctx, `SELECT server_name, transcoding, tls_mode, acme_provider, acme_domain, ddns_provider, hardware_decoding FROM server_settings WHERE id = 1`).
		Scan(&s.ServerName, &s.Transcoding, &s.TLSMode, &s.ACMEProvider, &s.ACMEDomain, &s.DDNSProvider, &s.HardwareDecoding); err != nil {
		return Settings{}, err
	}
	if u.ServerName != nil {
		s.ServerName, err = ValidateServerName(*u.ServerName)
		if err != nil {
			return Settings{}, err
		}
	}
	if u.Transcoding != nil {
		if !slices.Contains(Transcoding, *u.Transcoding) {
			return Settings{}, ErrInvalidTranscoding
		}
		s.Transcoding = *u.Transcoding
		// A vendor selects the complete automatic GPU pipeline. Keep the stored
		// decoding field for older API clients; new vendor updates enable it.
		s.HardwareDecoding = s.Transcoding != "software"
		// Going back to software takes decoding with it.
		if s.Transcoding == "software" {
			s.HardwareDecoding = false
		}
	}
	if u.TLSMode != nil {
		if !slices.Contains(TLSModes, *u.TLSMode) {
			return Settings{}, ErrInvalidTLSMode
		}
		s.TLSMode = *u.TLSMode
	}
	if u.ACMEProvider != nil {
		if !slices.Contains(ACMEProviders, *u.ACMEProvider) {
			return Settings{}, ErrInvalidACME
		}
		s.ACMEProvider = *u.ACMEProvider
	}
	if u.ACMEDomain != nil {
		if d := *u.ACMEDomain; d != "" && (len(d) > 253 || !domainPattern.MatchString(d)) {
			return Settings{}, ErrInvalidDomain
		}
		s.ACMEDomain = *u.ACMEDomain
	}
	if u.DDNSProvider != nil {
		if !slices.Contains(DDNSProviders, *u.DDNSProvider) {
			return Settings{}, ErrInvalidDDNS
		}
		s.DDNSProvider = *u.DDNSProvider
	}
	if u.HardwareDecoding != nil {
		if *u.HardwareDecoding && s.Transcoding == "software" {
			return Settings{}, ErrDecodeNeedsEncode
		}
		s.HardwareDecoding = *u.HardwareDecoding
	}

	if _, err := tx.ExecContext(ctx, `UPDATE server_settings SET server_name = ?, transcoding = ?, tls_mode = ?, acme_provider = ?, acme_domain = ?,
		ddns_provider = ?, hardware_decoding = ?, updated_at_unix = unixepoch() WHERE id = 1`,
		s.ServerName, s.Transcoding, s.TLSMode, s.ACMEProvider, s.ACMEDomain, s.DDNSProvider, s.HardwareDecoding); err != nil {
		return Settings{}, err
	}
	return s, tx.Commit()
}

// TunedThreads is the transcode thread count the server measured for itself and
// when (zero until the first measurement).
func TunedThreads(ctx context.Context, db *sql.DB) (threads int, at time.Time, err error) {
	var unix int64
	err = db.QueryRowContext(ctx, `SELECT tuned_threads, tuned_at_unix FROM server_settings WHERE id = 1`).Scan(&threads, &unix)
	if unix > 0 {
		at = time.Unix(unix, 0)
	}
	return threads, at, err
}

func SaveTunedThreads(ctx context.Context, db *sql.DB, threads int, at time.Time) error {
	_, err := db.ExecContext(ctx, `UPDATE server_settings SET tuned_threads = ?, tuned_at_unix = ? WHERE id = 1`, threads, at.Unix())
	return err
}

// ValidateServerName normalizes surrounding whitespace; names are labels, not DNS hosts.
func ValidateServerName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || utf8.RuneCountInString(value) > 64 || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return "", ErrInvalidServerName
	}
	return value, nil
}
