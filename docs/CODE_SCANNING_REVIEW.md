# Code scanning review

This review covers the 14 CodeQL findings reported against `7ae6fa8` in AlreadyOnTV/AlreadyOn. Dispositions summarize code changes and contextual review; GitHub is the source of truth for alert state. Read [security boundaries](SECURITY_BOUNDARIES.md) for compatibility and operational limits.

| Alerts | Finding | Response |
| --- | --- | --- |
| 1 | Client redirect | Compare parsed URL origin with the current origin. Regressions include browser normalization and permitted local routes. |
| 2 | Sensitive-data hashing | Salted Argon2id for chosen local testing keys; retain indexed SHA-256 only for generated 256-bit random keys. Legacy testing hashes no longer authenticate and are removed during local reprovisioning. |
| 3, 4 | Root-selection paths | Intentional owner boundary selection; playback/scanning enforce root confinement. The auth middleware denies viewers. |
| 5, 8 | Folder-browser paths | Owner-only browsing returns names/paths so owners can select new roots; viewers are denied. |
| 6, 7 | Static-file paths | Root-confined file opens replace joined-path stat and file delivery. Regression coverage rejects traversal and escaping symlinks while preserving static assets and SPA routes. |
| 9, 10 | Reflected XSS in media writers | Binary media uses inert content type and `nosniff`; synthetic script bytes test both headers. |
| 11 | Sample-rate conversion | Parse at native integer width; negative and overflowing metadata become unknown. |
| 12–14 | Memory-counter conversions | Guard multiplication and signed conversions; subtract cache without wrapping and handle invalid readings. |

Owner-path alerts reflect owner capability; the chooser returns names only and playback/scanning confine roots. Media alerts are mitigated by inert headers and tests. Record rationale with any dismissal.

CodeQL runs on dev, test and main. On 2026-10-10, alerts #3, #4, #5 and #8 were dismissed as won't fix; #9 and #10 as false positives. GitHub reported no open alerts after these dispositions; verify hosted scans on later commits.

The historical remediation included focused regressions, 32-bit sample-rate coverage, web build, lint, documentation and syntax checks.
