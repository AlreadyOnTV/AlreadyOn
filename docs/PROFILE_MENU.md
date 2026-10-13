# Profile menu

WebUI shows the selected profile’s avatar and name together in the top-right header on Guide, Dashboard, Profile Settings, Admin Settings and Server Settings. Clicking either opens the same menu. Long names truncate on narrow screens; the complete name remains the button’s accessible label and tooltip.

The button supports keyboard focus and Enter/Space. Tab moves through menu controls; Escape or clicking outside closes it. Switching profiles updates the avatar/name and returns to the Guide, retaining existing profile PIN checks.

Menu order is Dashboard, Profile Settings, Switch Profile, Admin Settings, Server Settings, Sign Out. Every signed-in profile can access Dashboard, Profile Settings and Switch Profile. Admin Settings appears for profile admins; Server Settings appears for server owners. Existing page-level permissions still apply.

The WebUI header change does not modify native phone/TV menus. See [development setup](DEVELOPMENT.md) for the shared menu order.
