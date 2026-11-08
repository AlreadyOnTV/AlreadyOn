# Release naming

Release cycles use `YY.MM.DD`, for example `26.10.18`. Two-digit years mean 2000–2099, without the usual date-parser century pivot. Writers compare canonical date prefixes, so a valid short year `20` is not confused with the old century prefix. Calendar dates and audit timestamps still use ISO 8601. Server build identities remain `YY.MM.DD_shortsha`, distinct from release-cycle labels.

Nightly dates are generated in `America/New_York` from the main merge time. Image aliases include `nightly` and `YY.MM.DD`; APK and release names append `-nightly` or `-stable`. Historical alpha/RC labels remain readable by installed clients but are no longer published. Keep the product, repository, container package and installed Android application IDs unchanged.

Clients recognize dotted labels and historical hyphenated formats in signed metadata. Older APK updaters that accept only four-digit dates need a manual signed update, preserving app data.

The Nightly workflow derives the current date and may replace that date's release/tag after build, test and security gates pass. It rebuilds/signs APKs with higher codes and regenerates checksums, inventories and source provenance. The Stable workflow reuses the latest Nightly image digest as `latest` and creates fresh Stable APKs. Never edit assets or refs manually.

See [delivery](CI_CD.md) and [Android releases](ANDROID_RELEASES.md).
