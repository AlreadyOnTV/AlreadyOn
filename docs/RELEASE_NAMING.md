# Release naming

Release cycles use `YY.MM.DD`, for example `26.10.18`. Two-digit years mean 2000–2099, without the usual date-parser century pivot. Writers compare canonical date prefixes, so a valid short year `20` is not confused with the old century prefix. Calendar dates and audit timestamps still use ISO 8601. Server build identities remain `YY.MM.DD_shortsha`, distinct from release-cycle labels.

The local/remote cycle branch, `release-date.txt`, source Git tag, GHCR date tag, APK version name for new builds and GitHub Release tag share that label. Titles append `-alphaN`, `-rcN` or `-stable`. Existing stage-tag casing (`RCN`, `Stable`) stays compatible; this migration shortens the year and changes date separators to periods. The product, repository, container package and installed Android application IDs retain their names.

Clients recognize dotted labels and historical hyphenated formats in signed metadata. Older APK updaters that accept only four-digit dates need a manual signed update, preserving app data.

Never rebuild or edit previously signed APKs, checksums or inventories to rename a release. Their original version strings, source commits and hashes are immutable audit identifiers. Rename the public release and its links, retaining release IDs and asset IDs/bytes. Retag container manifests with identical digests; never delete a digest still serving another tag. Validate short-name parsing, ordering, candidate allocation, retention, promotion and update URLs. Read historical source policy dates without weakening provenance gates.

See [delivery](CI_CD.md) and [Android releases](ANDROID_RELEASES.md).
