# Same-date Stable reissue

Use this only to finalize the active date after at least one RC has been published, when no Stable release or Stable Git tag exists for that date. It does not replace an existing release or rewrite an APK.

## Build the tested image

Merge the final dated-branch PR to `dev` through normal PR checks. Dispatch **Build Date Release Image** on `dev` with `publication=stable-reissue`, the exact current dev SHA, and the active `YY.MM.DD` date. The workflow runs quality, CodeQL, Android checks, full Go/browser/runtime tests, and vulnerability/secret scans. It builds and attests a fresh image, records the commit-to-digest status and run-linked authorization marker, and advances the movable date source tag. It publishes no APK and moves no Docker channel tag.

The preflight requires a same-date published RC whose source is an ancestor of the selected commit. It also requires no same-date Stable release/tag and no prior reissue marker for the commit. The marker is written only after the full workflow and image attestations succeed.

## Promote the exact commit

After reviewing the successful run, use `python3 scripts/promote.py test COMMIT`. The helper verifies the marker, image digest, date tag and RC history before advancing `test`. Test promotion updates `latest` and deliberately skips another RC APK build.

Then use `python3 scripts/promote.py main`. Main must promote the current test head. The image workflow verifies the same attested digest and synchronizes `nightly`, `latest` and `stable` to it. The Android workflow builds and signs fresh phone/TV Stable APKs from the marked commit, with higher version codes and the permanent signing identity; the existing RC remains unchanged.

Verify all three branch heads, the Stable release source and APK signatures/checksums, and the digest behind each channel tag. Remove the dated branch only after delivery verification and explicit cleanup approval. Never force-update branches, replace existing releases, or bypass a failed gate.
