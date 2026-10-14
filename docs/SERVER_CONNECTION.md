# Manual server connection

Phone and Google TV show a fixed grey `https://` prefix, a Hostname / IP textbox and a smaller Port textbox. Enter only the hostname or IP address, without a scheme, path or embedded port.

A valid IPv4 or IPv6 address pre-fills the visible Port field with `19900`. You can change or clear it. Hostnames have no automatic port suggestion. Switching from an automatically filled IP to a hostname clears that suggestion; an explicitly edited or cleared port is preserved while editing the address.

An empty port uses ordinary HTTPS port 443, including for IP addresses. It never silently falls back to 19900. Enter a custom port from 1 through 65535 if needed. IPv6 addresses accept optional brackets and are formatted correctly in the HTTPS URL. Invalid input is rejected before connecting.

Discovery remains active while typing. Check Server uses the existing app-local server trust flow; username and account TV PIN are requested only after checking the selected server. No device-wide CA import is needed. Saved trust failures still require explicit replacement acceptance.

Google TV’s server PIN dial pad measures 70% of its previous layout size, including spacing and typography. System font scaling remains respected. Remote focus, Clear, Delete, the 12-digit limit and Done behavior remain unchanged. Profile PIN dialogs and phone PIN entry are unchanged.
