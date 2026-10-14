# Saved API credentials

TMDb keys and Dynamic DNS credentials are never returned to the settings form. On a fresh load, saved-state placeholders remain masked and have no visibility button. Entering a replacement draft exposes its eye control, which shows/hides only the unsaved text. Clearing or saving the draft hides the control; a new draft starts masked. This presentation does not imply one-way hashing: the server must retain usable credentials to contact the providers.

## WebUI autofill policy

Only the actual Sign In form opts into browser/password-manager autofill. Setup, password changes, profile PINs, all settings and dynamically mounted dialogs request `autocomplete=off` with Bitwarden, LastPass and 1Password ignore hints. An application-wide guard rejects script-generated input/change events outside Sign In, preserving the last accepted value and preventing those events from dirtying settings. Real keyboard entry and paste remain available; saved secrets are never exposed by these hints.

A website cannot guarantee control over browser or privileged extension behavior. A manager that ignores hints, modifies attributes or simulates trusted browser input can bypass website suppression. Actual browser/extension settings govern a complete block. The application never disables password-manager support on Sign In.
