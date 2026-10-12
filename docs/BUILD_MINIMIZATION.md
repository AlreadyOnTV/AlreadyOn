# Build minimization

Production artifacts retain supported codecs, GPU drivers, Android architectures, TLS, authentication and recovery commands. Build/test tools stay in CI.

## Android

Phone and TV release builds use R8 code optimization and integrated resource shrinking. Debug builds retain their usual diagnostics. The application identity, version inputs and signing process are unchanged.

Comparable unsigned APKs with identical version/toolchain inputs:

| Artifact | Before | After | Reduction |
| --- | ---: | ---: | ---: |
| Phone | 10,912,462 bytes | 2,077,077 bytes | 81.0% |
| TV | 2,492,033 bytes | 2,078,457 bytes | 16.6% |

Sizes exclude signing. Both retain native libraries and passed emulator launch checks; physical acceptance remains required.

## Containers

The runtime removes unused Pebble, curl and vainfo. A final scratch stage copies the cleaned filesystem, retaining Ubuntu libraries, package metadata, certificates and time zones. AlreadyOn supplies its own TLS health command and hardware probing.

The server and local administration commands share one executable through filename aliases, avoiding duplicate Go/SQLite runtime copies. Standalone developer commands remain available. Password-reset regression tests exercise the packaged dispatch against temporary accounts.

Copyright notices and FFmpeg build/license metadata remain. Unused package documentation is removed. CI uses Chromium's headless shell and removes installation caches and unused package-manager shims.

Local candidate source `fd0f576` compared with the published alpha2 image:

| Measurement | Before | After |
| --- | ---: | ---: |
| Runtime compressed layers | 210,154,355 bytes | 186,492,160 bytes |
| Runtime packages | 185 | 164 |
| CI scanner image bytes | 1,869,779,456 | 1,391,099,392 |

Runtime scan counts fell from 3 High/42 Medium/7 Low/9 Unknown to 0 High/37 Medium/6 Low/1 Unknown. CI fell from 11 High to 8 High; vulnerable build-tool dependencies still require remediation. Source retains one Unknown finding. No Critical findings or secret matches were detected. These report-only results use Trivy 0.75.0; coverage limits remain.

## Evidence and publication

Retain private hashes/timestamps and validate media and the disposable runtime before delivery.

Public inventory SBOMs are approved for release attachments. Generate them separately from private vulnerability/secret reports, bind them to image digests or APK checksums, and retain the same SBOM bytes when promoting tested artifacts. See [scanning](VULNERABILITY_SCANNING.md) and [release gates](CI_CD.md).

CI now pins npm 11.21.0 with locked patched dependencies. Its rescan found zero Critical/High/secret matches, with 42 Medium and 10 Low remaining. Public SBOMs use allowlisted inventories bound through release provenance.
