# Security boundaries

Owners browse library roots using server filesystem permissions. Viewers cannot browse or change these settings. Browsing returns directory names and paths, never file contents. Playback separately confines file opens to configured roots, including symlink checks. Limit server filesystem access.

Web assets are opened inside the installed web directory with `os.OpenInRoot`. Traversal and escaping symlinks cannot expose outside files. Relative symlinks within the directory work; absolute symlinks are refused. Client routes retain the application-shell fallback and hashed assets retain immutable caching.

Sign-in return URLs are parsed using the browser URL parser and compared with the current origin before navigation. Checking a raw string prefix alone is insufficient because URL parsing normalizes control characters.

Passwords and account PINs use Argon2id (19 MiB, two passes, one lane, 16-byte random salt, 32-byte output). Four concurrent hashes bound memory use; parameters are stored in each hash.

Generated management keys contain 256 random bits and retain indexed SHA-256 verification. Chosen local testing keys use salted Argon2id and the bounded password-hashing pool. Verification examines only testing records. Keep their number small; use generated keys on deployed servers. Legacy local test-key hashes do not authenticate; local reprovisioning removes those obsolete rows, so old test keys must be provisioned again. No default keys. Revocation and enabled-owner checks apply to both types.

The existing key table stores generated-key SHA-256 and testing-key Argon2id hashes without a schema migration. Older binaries cannot authenticate newly provisioned Argon2id testing keys; generated keys remain compatible. Legacy testing hashes fail closed and are removed on local reprovisioning.

Direct-playback responses explicitly use `application/octet-stream` with `nosniff`; manifests and segments retain their specific media types, and subtitle cues are JSON. Generic response wrappers forward bytes without converting them to HTML. Byte-range support and idle write deadlines remain active.

Memory counters are checked before unsigned-to-signed conversions and multiplication. Invalid host readings are unavailable, invalid cgroup readings fall back to host readings, and cache subtraction cannot wrap. Sample rates are parsed at the target architecture's integer width; negative or overflowing values mean unknown.

CodeQL runs on dev PRs and pushes to dev, test and main. A local regression pass does not prove alerts have disappeared: verify hosted analysis on reviewed commits. Owner-controlled root selection and generic response wrappers need contextual review when reported.

Redirect coverage runs with `cd web && npm run test:security`. Go regressions use temporary databases and synthetic files. Follow [the security policy](../SECURITY.md) when reporting vulnerabilities.
