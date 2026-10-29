# TLS and remote access

[![AlreadyOn](../web/public/branding/header-doc.svg)](../README.md)

UI, API and media share HTTPS port 19900 by default. HTTP-01 uses port 19902 only for certificate validation.

## Local installations

An instance can provision a local certificate authority for first-run access. Trust that instance's CA only on devices you control. Verify the hostname covered by its certificate. Android onboarding accepts the selected instance CA inside the app without device-wide import. LAN certificate-name exceptions are origin-scoped; sign-in and playback verify the chain and validity. Legacy unrestricted verification bypasses are ignored. Public bootstrap metadata carries no credentials, but first-use trust cannot detect impersonation; connect first on a trusted LAN.

Redeployments retain the database-backed CA and native sessions; persist `/config`. Renewed certificates under the same root and PEM formatting changes need no new trust. TV certificate failures open that server's credentials directly with a red warning, not a review popup. **Accept New CA** replaces **Connect**, requires username/PIN, and explicitly accepts trust before signing in in one action. Phone recovery retains **Review Server CA** and separate acceptance. Going back without accepting preserves old trust. Owners must renew expired certificates.

TV uses the installed numeric keyboard for the visible **TV Pairing Pin**, without an eye control or prescribed initial digit. Address and credential screens omit the logo header and align content at the top, using the same modest top margin as other non-fullscreen TV screens so entered values and credential actions remain above the native keyboard. Signed-in screens retain their branding. **Connect**, **Go Back** and **Help** share one row; Go Back preserves address drafts, and Help identifies WebUI Settings as the credential source. Startup keeps the splash until saved session/profile/guide restoration and the first video frame, or a recoverable error; onboarding is not composed during restoration.

## Public access

Use a domain you control and a publicly trusted certificate. Configure DNS/firewall forwarding first. For direct access, publish host TCP/443 to container HTTPS port 19900 and host TCP/80 to container challenge port 19902, then forward WAN TCP/443 and TCP/80 to the matching host ports. HTTP-01 requires external port 80; the challenge listener is not a general HTTP redirect. Supported ACME options include HTTP-01 and Cloudflare DNS-01; choose the method appropriate for your deployment.

Keep DNS credentials private and minimally scoped. Validate certificate renewal before relying on automation.

## Proxy boundaries

Use TCP TLS passthrough to HTTPS port 19900, or HTTPS re-encryption with upstream certificate verification. Preserve the external Host header. For nginx HTTP proxying use `proxy_pass https://alreadyon:19900`, `proxy_set_header Host $http_host`, `proxy_ssl_verify on`, `proxy_ssl_trusted_certificate /etc/nginx/alreadyon-ca.pem`, `proxy_ssl_server_name on`, and `proxy_ssl_name` matching an upstream certificate name.

Forwarded, X-Forwarded-For/Host/Proto and X-Real-IP are ignored. No proxy address is authorized to supply client identity: authorization uses credentials and throttling uses the socket peer. Behind an HTTP proxy, users share that peer's throttle bucket; TLS passthrough also retains the proxy peer unless transparent routing preserves source addresses. Do not enable PROXY protocol or depend on forwarded headers. Restrict direct upstream access with the firewall. Plain HTTP upstream termination is unsupported for secure browser sessions; use HTTPS upstream or passthrough.

For testing, scoped loopback certificate exceptions are acceptable in tooling; they must not become a product-wide insecure mode. See [security](../SECURITY.md) and [deployment](../deploy/README.md).
