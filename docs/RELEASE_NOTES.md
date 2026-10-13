# Release notes

Release descriptions explain what users can do and what problems were fixed. Avoid raw commit messages, issue numbers without context, implementation jargon and CI transcripts.

For each user-facing change, add a uniquely named Markdown file under `docs/release-notes/` in the same commit. Use one or more of these headings:

```markdown
## Features

- Connect to a nearby server without typing its address.

## Improvements

- Show or hide the PIN you entered with the eye button.

## Bug fixes

- Keep mute and subtitle settings when changing channels fullscreen.
```

Use one complete sentence per bullet on a single line. Name the affected app or screen where useful. Describe visible behavior, not the code used to implement it. Omit empty categories and changes that only affect development tooling. Once an entry reaches stable, leave it intact and add a new file for later changes.

Android alpha publication reads only release-note files marked `<!-- android-release -->` that changed since the newest published Android release from an earlier date. Add that marker to phone/TV notes; unmarked server-only changes are excluded. Later alpha/RC notes include only marked notes since the previous alpha/RC. A maintenance-only artifact explicitly says there are no new app changes.

RC and Stable promotions copy the selected candidate’s changelog rather than reading newer source changes. They reuse the tested APK bytes, checksums, signatures and provenance. See [release policy](ANDROID_RELEASES.md). Download links appear below changes; source and build details remain collapsed.

Editing existing release descriptions does not rebuild APKs or change their source tags. Keep each description accurate for that release. Older candidates without structured notes need their descriptions updated before stable promotion.
