# TV profile settings

Open the profile menu and choose **Profile Settings**, between **Dashboard** and **Switch Profile**. The full-screen page stops playback and removes the player and guide. It uses the same current-profile API and fields as the WebUI; it does not expose administration of other profiles.

Use Up/Down to preview categories automatically. Select or Right focuses Name, the first icon, Set/Clear PIN, the time/audio dropdown or the subtitle-default toggle. Focus starts on **Display Name**. A white outline marks the focused control. Choice menus focus and mark the current value. Name and PIN fields use the TV’s on-screen keyboard.

Categories are Display Name, Icon, PIN, Time Format, Audio Language and Subtitles. Icon shows the current avatar with Remove Icon alongside and presets below. Upload custom images through the WebUI. Subtitles uses an On/Off by Default button plus a language dropdown.

**Save Changes** commits only changed name, icon, time-format and language/default-subtitle fields. Save captures the current category and moves focus there before disabling the button; success refreshes the draft and retains that category. The save confirmation appears below Save Changes and Cancel, and clears when another sidebar category is selected. Failed saves keep the draft and show the error. Drafts survive category changes and are discarded when leaving without saving. PIN updates and removal apply separately; removal requires confirmation. **Cancel** or **Back To Guide** returns to the guide, restores playback and focuses the watched channel. Remote Back first closes a popup or keyboard, then moves from controls to the current sidebar category; Back from the sidebar returns to the guide.

Only the current category is mounted; there are no animated assets or background guide refreshes while settings is open.

For a signed-in review device, run `python3 scripts/android/review-tv-profile-settings.py --serial DEVICE_SERIAL --sdk /path/to/Android/Sdk`. It checks menu placement, automatic categories, primary-control focus, subtitle draft toggling and two-stage Back to the sidebar and watched channel. It discards changes without saving or changing the PIN.
