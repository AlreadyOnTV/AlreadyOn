# AlreadyOn TV identity and release cycle

AlreadyOn remains the application name. The GitHub organization is `AlreadyOnTV`, with display name **AlreadyOn TV**. The application repository is `AlreadyOnTV/AlreadyOn`; the website host is `AlreadyOnTV/alreadyontv.github.io`, with canonical domain `alreadyontv.com`.

Container instructions use `ghcr.io/alreadyontv/alreadyon`. Existing installations must update their image reference when pulling a new version. Repository redirects do not guarantee package-registry compatibility. The Go module and Android package/metadata identifiers deliberately retain their existing identities so internal imports and installed-app updates remain compatible. Use the permanent signing key for every Android release. Older apps restrict update URLs to the former owner and may need one manual signed APK update; install over the existing app without clearing data.

The October 18 cycle includes all pending work on `2026-10-18`. To validate and publish its current alpha candidate without promoting dev/test/main:

```sh
gh workflow run rebuild.yml --repo AlreadyOnTV/AlreadyOn \
  --ref 2026-10-18 -f release_date=2026-10-18
```

The pipeline checks source freshness, runs quality, CodeQL, Go/browser/disposable-runtime and security gates, publishes signed image provenance/SBOMs, and builds signed Android candidates. Candidate numbering is allocated from the cycle’s published releases; the first is `2026-10-18-alpha1`. Dated dispatch retains the branch. A failed gate prevents later publication stages. If the branch advances, repeat validation for the new source.

Stable deployment is intended for October 18, 2026 after alpha/RC acceptance and the maintainer’s release approval. No schedule automatically promotes branches or deploys an unaccepted build. Promotion must reuse the exact tested image/APK artifacts and preserve live configuration and media.

Cloudflare DNS points the apex to GitHub Pages A/AAAA addresses and `www` to `alreadyontv.github.io`, using DNS-only records. GitHub Pages owns website TLS. The Pages domain-verification TXT record must remain in place; no wildcard records are needed. See [website maintenance](WEBSITE.md) and [delivery](CI_CD.md).
