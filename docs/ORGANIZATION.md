# AlreadyOn TV identity and release cycle

AlreadyOn remains the application name. The GitHub organization is `AlreadyOnTV`, with display name **AlreadyOn TV**. The application repository is `AlreadyOnTV/AlreadyOn`; the website host is `AlreadyOnTV/alreadyontv.github.io`, with canonical domain `alreadyontv.com`.

Container instructions use `ghcr.io/alreadyontv/alreadyon`. Existing installations must update their image reference when pulling a new version. Repository redirects do not guarantee package-registry compatibility. The Go module and Android package/metadata identifiers deliberately retain their existing identities so internal imports and installed-app updates remain compatible. Use the permanent signing key for every Android release. Older apps restrict update URLs to the former owner and may need one manual signed APK update; install over the existing app without clearing data.

Only `dev` and `main` are long-lived branches. Normal delivery follows this map:

| Branch | Docker channel | Phone/TV APK release |
| --- | --- | --- |
| `dev` | no published channel | local/USB validation |
| `main` merge | `nightly` and `YY.MM.DD` | `YY.MM.DD-nightly` |
| Manual Stable publication from latest Nightly | `latest` (same digest) | `YY.MM.DD-stable` (fresh APKs) |

The PR to `main` requires CodeQL, dependency review, and full Go/browser/runtime e2e checks. Main publication builds and scans the image, signs provenance, and publishes Nightly APKs. Weekly dependency updates are validated before integration to `dev`. Stable publication reuses the latest Nightly image digest and freshly builds Stable APKs with the permanent signing identity. Published artifact bytes and tags are not overwritten.

Cloudflare DNS points the apex to GitHub Pages A/AAAA addresses and `www` to `alreadyontv.github.io`, using DNS-only records. GitHub Pages owns website TLS. The Pages domain-verification TXT record must remain in place; no wildcard records are needed. See [website maintenance](WEBSITE.md) and [delivery](CI_CD.md).
