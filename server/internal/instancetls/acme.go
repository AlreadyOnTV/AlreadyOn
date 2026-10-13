package instancetls

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/logging"
	"golang.org/x/crypto/acme"
)

const productionDirectory = "https://acme-v02.api.letsencrypt.org/directory"
const stagingDirectory = "https://acme-staging-v02.api.letsencrypt.org/directory"

var publicHostname = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]([a-z0-9-]{0,61}[a-z0-9])?$`)

// ACMEStatus contains configuration and progress, never account/certificate keys.
type ACMEStatus struct {
	AgreementAccepted bool   `json:"agreementAccepted"`
	Configured        bool   `json:"configured"`
	Hostname          string `json:"hostname"`
	Email             string `json:"email"`
	Enabled           bool   `json:"enabled"`
	Running           bool   `json:"running"`
	LastAttempt       int64  `json:"lastAttempt"`
	LastSuccess       int64  `json:"lastSuccess"`
	NextAttempt       int64  `json:"nextAttempt"`
	Message           string `json:"message"`
	StagingHostname   string `json:"stagingHostname"`
}

// ParsePublicHostname accepts a bare DNS hostname or HTTPS URL, including its
// advertised port. Paths, credentials, IP literals and wildcard SANs are rejected.
func ParsePublicHostname(input string) (string, error) {
	input = strings.TrimSpace(input)
	if !strings.Contains(input, "://") {
		input = "https://" + input
	}
	u, err := url.Parse(input)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", errors.New("provide a public DNS hostname or HTTPS URL without credentials, path, query or fragment")
	}
	host := strings.ToLower(u.Hostname())
	if len(host) > 253 || !publicHostname.MatchString(host) || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || net.ParseIP(host) != nil {
		return "", errors.New("provide a public DNS hostname; IP addresses and wildcards are unsupported")
	}
	if u.Port() != "" {
		if _, err := net.LookupPort("tcp", u.Port()); err != nil {
			return "", errors.New("invalid HTTPS port")
		}
	}
	return host, nil
}

func (m *Manager) acmeStatus(ctx context.Context) ACMEStatus {
	var s ACMEStatus
	_ = m.db.QueryRowContext(ctx, `SELECT agreement_accepted,configured,hostname,email,enabled,last_attempt,last_success,next_attempt,message,staging_hostname FROM acme_tls WHERE id=1`).Scan(&s.AgreementAccepted, &s.Configured, &s.Hostname, &s.Email, &s.Enabled, &s.LastAttempt, &s.LastSuccess, &s.NextAttempt, &s.Message, &s.StagingHostname)
	m.challengeMu.RLock()
	s.Running = m.acmeRunning
	m.challengeMu.RUnlock()
	return s
}

// ChallengeHandler serves only currently registered HTTP-01 challenges. It has
// no management/login/media routes and never redirects or reflects arbitrary input.
func (m *Manager) ChallengeHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		m.challengeMu.RLock()
		response := m.challenges[r.URL.Path]
		host := m.challengeHost
		m.challengeMu.RUnlock()
		requestHost := r.Host
		if h, _, e := net.SplitHostPort(requestHost); e == nil {
			requestHost = h
		}
		if response == "" || !strings.EqualFold(requestHost, host) {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(response))
		}
	})
}

// RequestACME starts a bounded asynchronous order. The administrator must agree
// to terms; production is available only after a successful staging order for
// this exact hostname. No certificate is replaced during staging.
func (m *Manager) RequestACME(ctx context.Context, input, email string, agreed, staging bool) error {
	return m.requestACME(ctx, input, email, agreed, staging, false)
}

// SaveACME uses the configured DDNS hostname and saves consent independently of
// the active identity. Revocation stops renewal without discarding its certificate.
func (m *Manager) SaveACME(ctx context.Context, input, email string, agreed bool) error {
	if !agreed {
		if !m.acmeMu.TryLock() {
			return errors.New("an ACME attempt is already running")
		}
		defer m.acmeMu.Unlock()
		if m.acmeStatus(ctx).Running {
			return errors.New("an ACME attempt is already running")
		}
		_, err := m.db.ExecContext(ctx, `UPDATE acme_tls SET agreement_accepted=0,enabled=0,configured=1 WHERE id=1`)
		return err
	}
	var provider string
	if err := m.db.QueryRowContext(ctx, `SELECT ddns_provider FROM server_settings WHERE id=1`).Scan(&provider); err != nil {
		return err
	}
	if provider != "none" {
		if err := m.db.QueryRowContext(ctx, `SELECT hostname FROM ddns_configs WHERE provider=?`, provider).Scan(&input); err != nil {
			return err
		}
	}
	return m.requestACME(ctx, input, email, agreed, false, true)
}
func (m *Manager) requestACME(ctx context.Context, input, email string, agreed, staging, automatic bool) error {
	host, err := ParsePublicHostname(input)
	if err != nil {
		return err
	}
	addr, err := mail.ParseAddress(email)
	if !agreed || err != nil || addr.Address != email || len(email) > 254 {
		return errors.New("provide a contact email and accept the Let's Encrypt Subscriber Agreement")
	}
	if !m.acmeMu.TryLock() {
		return errors.New("an ACME attempt is already running")
	}
	defer m.acmeMu.Unlock()
	s := m.acmeStatus(ctx)
	if s.Running {
		return errors.New("an ACME attempt is already running")
	}
	if time.Now().Unix() < s.NextAttempt && !(!staging && s.Message == "Staging validation succeeded; ready for production issuance") {
		return errors.New("ACME retry cooldown is active; wait until the next attempt time")
	}
	m.mu.RLock()
	activeHost := ""
	if m.mode == "acme" {
		activeHost = m.material.certificate.Leaf.DNSNames[0]
	}
	m.mu.RUnlock()
	if !automatic && !staging && s.StagingHostname != host && activeHost != host {
		return errors.New("run a successful staging validation for this hostname before production issuance")
	}
	if _, err = m.db.ExecContext(ctx, `UPDATE acme_tls SET agreement_accepted=1,configured=1,hostname=?,email=?,last_attempt=?,next_attempt=?,message=? WHERE id=1`, host, email, time.Now().Unix(), time.Now().Add(time.Hour).Unix(), "Starting Let's Encrypt validation"); err != nil {
		return err
	}
	m.challengeMu.Lock()
	m.acmeRunning = true
	m.challengeMu.Unlock()
	go m.performACME(host, email, staging, automatic && s.StagingHostname != host && activeHost != host)
	return nil
}

// performACME owns acmeMu throughout issuance so upload/recovery cannot be
// overwritten by an in-flight order. Failure retains the previous TLS identity.
func (m *Manager) performACME(host, email string, staging, validateFirst bool) {
	m.acmeMu.Lock()
	defer m.acmeMu.Unlock()
	defer func() {
		m.challengeMu.Lock()
		m.acmeRunning = false
		m.challenges = nil
		m.challengeHost = ""
		m.challengeMu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	logging.Info("Let's Encrypt: starting HTTP-01 validation (staging=%v)", staging)
	var err error
	if validateFirst {
		err = m.issueACME(ctx, host, email, true)
	}
	if err == nil {
		err = m.issueACME(ctx, host, email, staging)
	}
	message := "Certificate activated; automatic renewal enabled"
	if staging {
		message = "Staging validation succeeded; ready for production issuance"
	}
	if err != nil {
		// CA errors can include challenge bodies/URLs. Keep public/log errors bounded
		// and generic; administrators receive actionable routing guidance instead.
		message = "Let's Encrypt validation failed; check public A/AAAA DNS and WAN TCP/80 forwarding to challenge port 19902. Existing certificate retained."
		logging.Warn("Let's Encrypt: attempt failed (staging=%v); existing certificate retained", staging)
	} else {
		logging.Info("Let's Encrypt: attempt succeeded (staging=%v)", staging)
	}
	if err == nil {
		_, _ = m.db.ExecContext(ctx, `UPDATE acme_tls SET message=?,last_success=?,next_attempt=? WHERE id=1`, message, time.Now().Unix(), time.Now().Add(24*time.Hour).Unix())
	} else {
		next := time.Now().Add(time.Hour)
		var ae *acme.Error
		if errors.As(err, &ae) {
			if d, _ := acme.RateLimit(err); d > 0 && time.Now().Add(d).After(next) {
				next = time.Now().Add(d)
			}
		}
		_, _ = m.db.ExecContext(context.Background(), `UPDATE acme_tls SET message=?,next_attempt=? WHERE id=1`, message, next.Unix())
	}
}

func (m *Manager) issueACME(ctx context.Context, host, email string, staging bool) error {
	column, directory := "account_key", productionDirectory
	if staging {
		column, directory = "staging_key", stagingDirectory
	}
	var encoded []byte
	if err := m.db.QueryRowContext(ctx, `SELECT `+column+` FROM acme_tls WHERE id=1`).Scan(&encoded); err != nil {
		return err
	}
	var account *ecdsa.PrivateKey
	if len(encoded) == 0 {
		var err error
		account, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return err
		}
		encoded, err = x509.MarshalECPrivateKey(account)
		if err != nil {
			return err
		}
		if _, err = m.db.ExecContext(ctx, `UPDATE acme_tls SET `+column+`=? WHERE id=1`, encoded); err != nil {
			return err
		}
	} else {
		var err error
		account, err = x509.ParseECPrivateKey(encoded)
		if err != nil {
			return err
		}
	}
	client := &acme.Client{Key: account, DirectoryURL: directory, UserAgent: "AlreadyOn ACME", HTTPClient: &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return errors.New("ACME redirects refused") }}}
	if _, err := client.Register(ctx, &acme.Account{Contact: []string{"mailto:" + email}}, acme.AcceptTOS); err != nil && !errors.Is(err, acme.ErrAccountAlreadyExists) {
		return err
	}
	order, err := client.AuthorizeOrder(ctx, acme.DomainIDs(host))
	if err != nil {
		return err
	}
	for _, authURL := range order.AuthzURLs {
		auth, err := client.GetAuthorization(ctx, authURL)
		if err != nil {
			return err
		}
		if auth.Status == acme.StatusValid {
			continue
		}
		var challenge *acme.Challenge
		for _, c := range auth.Challenges {
			if c.Type == "http-01" {
				challenge = c
				break
			}
		}
		if challenge == nil {
			return errors.New("HTTP-01 unavailable")
		}
		response, err := client.HTTP01ChallengeResponse(challenge.Token)
		if err != nil {
			return err
		}
		m.challengeMu.Lock()
		m.challengeHost = host
		m.challenges = map[string]string{client.HTTP01ChallengePath(challenge.Token): response}
		m.challengeMu.Unlock()
		if _, err = client.Accept(ctx, challenge); err != nil {
			return err
		}
		if _, err = client.WaitAuthorization(ctx, authURL); err != nil {
			return err
		}
		m.challengeMu.Lock()
		m.challenges = nil
		m.challengeMu.Unlock()
	}
	order, err = client.WaitOrder(ctx, order.URI)
	if err != nil {
		return err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{DNSNames: []string{host}}, key)
	if err != nil {
		return err
	}
	chain, _, err := client.CreateOrderCert(ctx, order.FinalizeURL, csr, true)
	if err != nil {
		return err
	}
	if staging {
		_, err = m.db.ExecContext(ctx, `UPDATE acme_tls SET staging_hostname=? WHERE id=1`, host)
		return err
	}
	var certPEM []byte
	for _, der := range chain {
		certPEM = append(certPEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	material, err := publicACMEMaterial(certPEM, keyPEM, host)
	if err != nil {
		return err
	}
	if !time.Now().Before(material.certificate.Leaf.NotAfter) || time.Now().Before(material.certificate.Leaf.NotBefore) {
		return errors.New("issued certificate is not current")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.activate(ctx, material, "acme", func() error {
		tx, err := m.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if _, err = tx.ExecContext(ctx, `UPDATE acme_tls SET certificate_pem=?,private_key_pem=?,active_hostname=?,enabled=1,active=1 WHERE id=1`, certPEM, keyPEM, host); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE uploaded_tls SET active=0 WHERE id=1`); err != nil {
			return err
		}
		return tx.Commit()
	})
}

// Public trust is checked against system roots, never a CA supplied by the
// remote order. Persisted expired identities can load for renewal/recovery.
func publicACMEMaterial(certPEM, keyPEM []byte, host string) (*Material, error) {
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	leaf := pair.Leaf
	if leaf == nil {
		leaf, err = x509.ParseCertificate(pair.Certificate[0])
		if err != nil {
			return nil, err
		}
		pair.Leaf = leaf
	}
	intermediates := x509.NewCertPool()
	for _, der := range pair.Certificate[1:] {
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, err
		}
		intermediates.AddCert(cert)
	}
	// Verify at a time inside the certificate lifetime on startup; issuance also
	// checks the current lifetime before activation in issueACME.
	when := time.Now()
	if when.After(leaf.NotAfter) {
		when = leaf.NotAfter.Add(-time.Second)
	}
	chains, err := leaf.Verify(x509.VerifyOptions{DNSName: host, Intermediates: intermediates, CurrentTime: when})
	if err != nil {
		return nil, fmt.Errorf("invalid public ACME chain: %w", err)
	}
	var trust []byte
	for _, c := range chains[0][1:] {
		trust = append(trust, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})...)
	}
	return &Material{certificate: pair, root: chains[0][len(chains[0])-1], rootPEM: trust}, nil
}

// RenewACME checks once per hour (and on startup), ordering only inside the
// 30-day renewal window. Persisted cooldown survives restarts and manual retries.
func (m *Manager) RenewACME(ctx context.Context) {
	s := m.acmeStatus(ctx)
	if !s.AgreementAccepted || !s.Enabled || s.Running || time.Now().Unix() < s.NextAttempt {
		return
	}
	m.mu.RLock()
	activeHost := ""
	if m.mode == "acme" {
		activeHost = m.material.certificate.Leaf.DNSNames[0]
	}
	due := m.mode == "acme" && time.Now().Add(30*24*time.Hour).After(m.material.certificate.Leaf.NotAfter)
	m.mu.RUnlock()
	if due {
		_ = m.RequestACME(ctx, activeHost, s.Email, true, false)
	}
}
