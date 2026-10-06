# Privacy and security signals

Default masking covers password/token/access/refresh, authorization and proxy authorization, cookies, API keys, secrets, client secrets, private keys, and common session/CSRF keys. Matching is case-insensitive and treats hyphens and underscores equivalently. Structured JSON is recursively masked, including arrays. Header/query values are masked before any queue, sink, or observer receives an event.

Field names cannot identify all sensitive data. Add application-specific `MaskKeys`, strip payloads on sensitive routes, and set an appropriate retention policy. Malformed, over-limit, excessively nested, or incomplete JSON has no raw-data fallback. Additional formats require an explicit safe sanitizer.

The embedded dashboard requires host application authorization. View, export, and delete are separate permissions. Same-origin protection applies to POST actions; payload rendering uses escaped templates, exports neutralize spreadsheet formulas, and pages prohibit framing and caching. Database credentials, raw SQL exceptions, and stored payloads are not part of doctor output.

## Optional detect-only rules

Security detection is off by default. Enable `cfg.Security.Enabled`; request and response sample inspection each require an additional opt-in. Samples are bounded and temporary. Actor correlation uses an in-process HMAC fingerprint with bounded expiring state; fingerprints are not exported.

| ID | Signal |
| --- | --- |
| DRFSEC-001 | Authentication failure |
| DRFSEC-002 | Success after at least three recent authentication failures |
| DRFSEC-003 | Failure on token/auth routes |
| DRFSEC-004 | Authorization failure |
| DRFSEC-005 | Admin/debug route access |
| DRFSEC-006 | Suspicious request sample patterns |
| DRFSEC-007 | HTTP 429 pressure |
| DRFSEC-008 | HTTP 404 route-scan hint |
| DRFSEC-009 | Repeated object-ID probes |
| DRFSEC-010 | Request sample line-control characters |
| DRFSEC-011 | Credential-like field names in response samples |
| DRFSEC-012 | Repeated pagination/cursor requests |
| DRFSEC-013 | Export/download/report route or action |
| DRFSEC-014 | Response sample at least 8 KiB |
| DRFSEC-015 | Application-declared sensitive route |
| DRFSEC-016 | Repeated application-declared business flow/action |

`Security.Rules` accepts selected IDs; nil enables all rules when security is enabled, and an empty slice enables none. `SetContext` supplies `actor_id`, `business_action`, `flow_name`, and string `is_sensitive_route="true"` when relevant. No direct identity is required.

These are inexpensive investigation hints, including simple status/route matches. They do not establish attacks, block traffic, provide a WAF, or offer compliance certification. Inspect false positives before creating alerts. No SQL argument values or database result rows are stored by profiling.
