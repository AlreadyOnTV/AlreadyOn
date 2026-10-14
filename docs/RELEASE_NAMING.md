# Release naming

Release cycles use `YY-MM-DD`, for example `26-10-18`. Two-digit years mean 2000–2099, without the usual date-parser century pivot. Calendar dates and audit timestamps still use ISO 8601.

The local/remote cycle branch, `release-date.txt`, source Git tag, GHCR date tag, APK version name for new builds and GitHub Release tag share that label. Titles append `-alphaN`, `-rcN` or `-stable`. Existing stage-tag casing (`RCN`, `Stable`) stays compatible; this migration changes the year only. The product, repository, container package and installed Android application IDs retain their names.

The October 18 migration renames all existing public date labels directly, without compatibility aliases, as approved by the sole installed-app tester. Older APK updaters only accept four-digit dates; manually install a new client before using the short-tag updater. New clients recognize both formats in signed historical metadata.

Never rebuild or edit previously signed APKs, checksums or inventories to rename a release. Their original version strings, source commits and hashes are immutable audit identifiers. Rename the public release and its links, retaining release IDs and asset IDs/bytes. Retag container manifests with identical digests; never delete a digest still serving another tag. Validate short-name parsing, ordering, candidate allocation, retention, promotion and update URLs. Read historical source policy dates without weakening provenance gates.

The manual **Migrate release image date labels** workflow previews by default. Its approved apply mode moves obsolete aliases onto empty, unique placeholders, deletes only those placeholders, and verifies the new tags retain the original digest. This avoids deleting shared channel images.

See [delivery](CI_CD.md) and [Android releases](ANDROID_RELEASES.md).
