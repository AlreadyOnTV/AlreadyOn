# Create a profile from Switch Profile

In WebUI, an Admin profile can open the top-right profile control, choose Switch Profile, then select Add Profile beside Back. Viewer profiles do not see this action; native phone/TV menus are unchanged.

The menu closes and a centered modal asks for Name, Role (Admin or Viewer) and Audience (Kids, Teen or Unrestricted). Both dropdowns sort their labels alphabetically; defaults remain Viewer and Unrestricted. Create saves a trimmed, nonempty name through the existing authenticated profile API for the signed-in account. The current profile stays active. On success, the modal closes and the new profile is immediately available in Switch Profile without reloading. Cancel or Escape closes without creating anything; failed creation retains the form and shows an error.

Switch Profile places the current profile first and sorts all remaining names alphabetically, ignoring case. Existing avatars, roles and PIN-protected switching continue to work. The dialog traps keyboard focus and returns it to the profile control on close. It follows the WebUI autofill-suppression policy.

Account ownership and server API permissions remain unchanged. This is a WebUI shortcut to the existing profile-creation workflow.
