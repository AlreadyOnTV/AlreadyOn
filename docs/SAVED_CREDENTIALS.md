# Saved API credentials

TMDb keys and Dynamic DNS credentials are never returned to the settings form. On a fresh load, saved-state placeholders remain masked and have no visibility button. Entering a replacement draft exposes its eye control, which shows/hides only the unsaved text. Clearing or saving the draft hides the control; a new draft starts masked. This presentation does not imply one-way hashing: the server must retain usable credentials to contact the providers.
