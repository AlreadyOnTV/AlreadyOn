# AlreadyOn TV identity and release cycle

AlreadyOn remains the application name. The GitHub organization is `AlreadyOnTV`, with display name **AlreadyOn TV**. The application repository is `AlreadyOnTV/AlreadyOn`; the website host is `AlreadyOnTV/alreadyontv.github.io`, with canonical domain `alreadyontv.com`.

Container instructions use `ghcr.io/alreadyontv/alreadyon`. Existing installations must update their image reference when pulling a new version. Repository redirects do not guarantee package-registry compatibility. The Go module and Android package/metadata identifiers deliberately retain their existing identities so internal imports and installed-app updates remain compatible. Use the permanent signing key for every Android release. Older apps restrict update URLs to the former owner and may need one manual signed APK update; install over the existing app without clearing data.

The active checkout branch is `26.10.28`. Normal delivery follows the branch map:

| Branch | Docker channel | Phone/TV APK release |
| --- | --- | --- |
| `dev` | `nightly` | `YY.MM.DD-nightly` |
| `test` | `latest` | `YY.MM.DD-rc1` (then `rc2`, etc.) |
| `main` | `stable` | `YY.MM.DD-stable` |

Dev publication requires full quality, CodeQL, Go/browser/disposable-runtime, Android and security gates. Weekly dependency integration includes major versions, validates the combined commit, and advances only dev. The maintainer validates the nightly before requesting test/RC and main/Stable promotion. No schedule promotes those stages or deploys a build. Docker promotion reuses the tested digest; stage APKs build from the selected tested source with the permanent signing identity. Historical published tags retain their original casing.


Cloudflare DNS points the apex to GitHub Pages A/AAAA addresses and `www` to `alreadyontv.github.io`, using DNS-only records. GitHub Pages owns website TLS. The Pages domain-verification TXT record must remain in place; no wildcard records are needed. See [website maintenance](WEBSITE.md) and [delivery](CI_CD.md).
