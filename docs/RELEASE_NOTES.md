# Release descriptions

Release descriptions and reviewed known issues live in GitHub Releases. Do not write `docs/release-notes/` fragments or a repository known-issue list. Implementation notes belong in issues/PRs; review captures stay outside Git.

Titles are only `YY.MM.DD-alphaN`, `YY.MM.DD-rcN` or `YY.MM.DD-stable`. Preserve tag casing, installed APK versions, exact signed artifacts and hidden provenance.

Every published description starts with `# Download Now`, linking that release’s phone/tablet and TV APKs, followed by `# New Features`, `# Bugfixes` and `# Known Issues`, in that order. Empty sections use “No new features in this release.”, “No bugfixes in this release.” and “No known issues are currently listed for this release.” Bugfixes and known issues link exact GitHub issue titles; a fix may append a brief explanation after the link.

## Prepare a candidate

Review product changes since the predecessor published release, including server and WebUI changes. Use PRs, issues and the source comparison; do not copy raw commit subjects into user-facing notes. Include prioritized remaining bugs supported by evidence, not every feature request.

Create an unpublished GitHub Release draft for the next candidate tag, targeting the full candidate commit SHA, with no assets. Its body contains the three product sections above (Download Now links are generated), followed by:

```markdown
<!--
Notes source commit: `FULL_40_CHARACTER_SHA`
Notes baseline: `PREDECESSOR_RELEASE_TAG_OR_none`
-->
```

Use `none` only for a first release with no predecessor. Notes-only drafts have no APK versionCode and must not be mistaken for built candidates. The release helper checks the exact source, target and computed predecessor, validates section/link format, generates downloads/provenance, and uploads verified artifacts through the existing approved workflow. Missing or stale notes fail publication instead of silently publishing empty notes. Update the draft when the source or predecessor changes.

No new release is created by editing source documentation. Publication remains maintainer-controlled under [delivery](CI_CD.md).

RC/Stable promotion reads the tested candidate’s published description and preserves its known issues and exact artifact bytes. Identical-artifact promotions add no new changes. Published notes remain the source for reruns; never invent retrospective evidence or regenerate an APK to edit its description.
