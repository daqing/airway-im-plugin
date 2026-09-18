# Airway IM API Documentation

The canonical machine-readable client contract is embedded in
[`openapi.md`](openapi.md) as OpenAPI 3.1 YAML. It documents every currently
implemented public HTTP operation and the companion WebSocket event contract.

Additional endpoint-focused notes:

- [`me.md`](me.md) — authenticated user profile and credential handling
- [`messages.md`](messages.md) — message validation, idempotency, persistence,
  retry, and delivery semantics
- [`admin.md`](admin.md) — internal dashboard authentication, metrics, and registered identities

When an endpoint-focused guide and the implementation differ, update the
OpenAPI contract together with the code. macOS models and networking code
should be generated or reviewed against `openapi.md`.
