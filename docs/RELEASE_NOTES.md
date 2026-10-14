# Release notes

GitHub Release titles contain only `YY.MM.DD-alphaN`, `YY.MM.DD-rcN` or `YY.MM.DD-stable`. Tags also use two-digit years; preserve their existing stage casing. Naming migrations keep released APK bytes, version codes, signatures, hashes and provenance intact. Embedded historical version names are audit data, not mutable release labels.

Each description always renders exactly these sections, in this order:

```markdown
# Download Now

- [Phone / tablet APK](https://github.com/AlreadyOnTV/AlreadyOn/releases/download/YY.MM.DD-alphaN/alreadyon-android.apk)
- [Google TV APK](https://github.com/AlreadyOnTV/AlreadyOn/releases/download/YY.MM.DD-alphaN/alreadyon-tv.apk)

# New Features

- Connect to a nearby server without typing its address.

# Bugfixes

- [Bug: Guide fails to tune the selected channel](https://github.com/AlreadyOnTV/AlreadyOn/issues/123)

# Known Issues

No known issues are currently listed for this release.
```

Empty sections say “No new features in this release.”, “No bugfixes in this release.” or the Known Issues sentence above. Download Now links both APKs for that release’s exact tag. There are no introductory release sentences or visible build-details block. Invisible source/version/baseline metadata supports safe reruns and promotions; signed asset provenance remains independently verified.

For user-facing changes, add a uniquely named Markdown file under `docs/release-notes/` in the same commit. Use `# New Features` for new capabilities and improvements, and `# Bugfixes` for addressed defects. Bugfix bullets must link the included GitHub issue and use its exact title. Historical Features, Improvements and Bug fixes fragment headings remain readable. Use single-line bullets, omit development-only maintenance, and leave published fragments intact.

Candidate notes include product fragments changed since the previous published release tag, including server and WebUI changes. They read the exact source commit, not the working tree. When no predecessor exists, notes include the available source fragments. Legacy unlinked bugfix fragments must be reviewed and linked before publication; the generator refuses untracked bugfix bullets.

Maintain `docs/release-known-issues.md` with `# Known Issues` and linked issue-title bullets for confirmed remaining bugs prioritized for the next few builds/releases. Remove entries when fixed. The generator reads this reviewed list from the artifact source commit; it does not infer bugs from feature requests or retrospective reports. Historical descriptions list only bugs supported by evidence at their release date.

RC and Stable promotions reuse tested APK bytes, checksums, signatures and provenance. An unchanged promoted artifact has no new features or bugfixes relative to its predecessor, and retains the candidate’s reviewed Known Issues. See [release policy](ANDROID_RELEASES.md). Editing release descriptions never rebuilds APKs or changes tags.
