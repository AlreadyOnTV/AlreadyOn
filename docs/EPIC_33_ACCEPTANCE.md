# Authentication and secure deployment acceptance

The 26.10.18 cycle confirms Epic 33 implementation using source and targeted regressions. Release publication and physical-device certification are separate.

| Task | Implementation and evidence |
| --- | --- |
| 34: Initial owner/password hashing | Transactional first-run creation; salted Argon2id parameters in [security boundaries](SECURITY_BOUNDARIES.md). `auth/account_test.go` covers hashing and concurrent bootstrap. |
| 35: Owner/viewer authorization | Fail-closed route roles and account/profile isolation; `auth/middleware_test.go`, `httpapi/users_test.go`, `profiles_isolation_test.go` and OpenAPI. |
| 36: Session lifecycle | Expiry/revocation and active-media rejection; `auth/account_test.go`, `httpapi/auth_test.go`. Native sessions persist until revoked. |
| 37: Listener configuration | The maintainer-approved unified HTTPS listener supersedes separate management/media listeners; `TestMediaOriginSharesApplicationListener` and [TLS](TLS.md). |
| 38: Private TLS | Instance CA generation/renewal in `instancetls`; app-local trust requires no device-wide import. `ServerTlsTest` verifies accepted CA, foreign-CA rejection, LAN-name exceptions, expiry and origin confinement. |
| 39: Reverse proxy | Verified HTTPS upstream or TLS passthrough; preserved Host. Forwarded identity is deliberately unsupported. `TestReverseProxyHeadersCannotChangePeerOrOrigin` and `TestAPIKeyThrottle` reject spoofing and throttle evasion. See [TLS](TLS.md). |
| 130: Attempt throttling | Bounded peer-address limiter, expiry/reset and lockout tests in `auth`; chosen testing keys use Argon2id. |
| 131: Playback hardening | Root-confined file opens, protected artwork without listings, principal session quotas, HTTPS media, non-echoing key entry and authenticated API docs; playback/session/source, HTTP router/auth tests; non-echoing entry is inspected in `admincli/accesskey/main.go`. |
| 139: Browser sign-in | Server accounts, transactional bootstrap, cookies, CSRF, expiry and revocation; account/auth HTTP tests and [API compatibility](../api/COMPATIBILITY.md). Multiple accounts supersede the original single-account design. |
| 140: Native pairing | Username plus account PIN supersedes random-code/browser approval, per maintainer decision. Native persistence, account isolation and throttling are tested; [Google TV](GOOGLE_TV.md) documents behavior. |
| 173: API keys | Owner-backed generation/revocation and explicit local provisioning; `auth/apikey*_test.go` and HTTP API-key tests. |

Automatic LAN first-use trust is an explicit maintainer decision. It cannot detect impersonation during initial setup. Saved trust failures block connections and show a review action; replacing the CA requires explicit acceptance. The server-origin certificate-name exception is intentional. It does not disable device-wide HTTPS verification. Bootstrap clients fetch only public metadata without credentials or redirects. See [TLS](TLS.md).

Release suites, hosted checks, deployment and physical-device acceptance remain separate gates. Broader security review remains separate.

The external FFmpeg pathname reopen race remains accepted for read-only media mounts and trusted host writers, as recorded in the existing hardening review. Direct reads use root-confined opens.
