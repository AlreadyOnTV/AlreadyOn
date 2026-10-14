# Settings layout review

These captures show the settings headers both with the profile menu closed (`*-closed.png`) and open. Mobile captures use a 390px viewport; the desktop Server Settings pair uses 1280px. Every settings header keeps the profile avatar and name together on one line; long names truncate rather than wrap.

Accounts and Profiles keep their table text intact inside keyboard-accessible horizontal scroll regions. Scroll these regions to reach the remaining columns and actions. The page itself stays within the viewport.

Screenshots are illustrative local preview captures. Server IP addresses use the documentation-only address `192.0.2.10`; browser-session details are outside the captured viewport. No server configuration or account data was changed for these captures.

Validation covered closed/open menu geometry and viewport bounds at 320, 390, 768 and 1280px, plus production WebUI build, lint and browser-test syntax. This focused review does not represent a full release validation run.
