# Security

[![AlreadyOn](web/public/branding/header-doc.svg)](README.md)

Report suspected vulnerabilities privately through the repository's **Security → Advisories → Report a vulnerability** page when available. If private reporting is unavailable, open a minimal issue requesting a private contact without exploit details or sensitive data.

Include affected versions, a sanitized reproduction, expected/actual behavior and impact. Do not attach credentials, live media, private host addresses or database exports. Allow maintainers to coordinate a fix before public disclosure.

This project is actively developing; no independent security certification or formal support window is claimed. Use trusted TLS, minimum necessary access and read-only media mounts. Keep credentials and application data outside Git.

Security fixes should include focused regression tests and a review of related entry points. Dependency, authorization, path confinement and media-processing boundaries require ongoing maintenance.

See [vulnerability scanning](docs/VULNERABILITY_SCANNING.md) for private reports, exact-image scans, coverage limits and proposed triage policy.
