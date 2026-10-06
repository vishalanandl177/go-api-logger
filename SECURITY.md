# Security policy

Please report suspected vulnerabilities privately using this repository's GitHub security advisory reporting when available, or the maintainer contact listed on the GitHub profile. Do not post real credentials, production payloads, or customer records in public issues.

Security updates target the latest stable release and the supported Go release lines. Dependency and Go runtime updates are checked by CI. A passing vulnerability scan is evidence about known advisories, not a security certification.

The dashboard requires application-provided authorization. Keep it behind your normal authentication and transport protections. Redaction is key-based and cannot infer every application-specific secret; configure additional mask keys and metadata-only routes. See [privacy and security configuration](docs/security.md).
