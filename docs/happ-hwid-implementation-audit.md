# Happ HWID — implementation inventory and protected subscription pages

This repository **already contained** the Happ-compatible device registration
mechanism before this patch. Do not reimplement it in ElXray:

- `internal/sub/controller.go` reads `X-HWID`, `X-Device-OS`,
  `X-Ver-OS`, `X-Device-Model` and `User-Agent`.
- `internal/web/service/client_hwid.go` enforces per-`sub_id`
  subscription-download limits, preserves SHA-256 hashes instead of raw HWIDs,
  records last-seen and device metadata, and supports listing/removing devices.
- `internal/database/model/model.go` already contains `ClientHwid`,
  `LimitHwid` and their GORM persistence fields.
- `frontend/src/components/clients/ClientHwidList.tsx` already contains
  the administrative device list.
- `internal/sub/happ.go` already supports Happ subscription response headers
  including `Subscription-Always-Hwid-Enable`.

## Small security correction in this PR

The raw, JSON, Clash and Mihomo subscription-body endpoints were already
HWID-gated (including `?view=raw`). The HTML subscription page was not:
it could render importable proxy URLs for an unauthenticated browser.
`maybeServeSubPage` now invokes the existing gate before rendering any
HTML with importable links, including content negotiation and `?html=1` or
`?view=html`. Under a nonzero HWID limit, browsers without HWID receive
404 and standard Happ-compatible HWID denial headers. Under limit 0,
backward compatibility is unchanged.

`?format=info` is still SELECT-only and intentionally strips the `links`
field; this endpoint must not spend a device slot or expose proxy configs.
Regression tests cover all HTML aliases, known/unknown HWIDs, and
read-only info.

## Important limits (not solved by this small patch)

The current 3X-UI-derived implementation limits *subscription downloads*,
not use of a previously copied VLESS UUID. The same VLESS UUID can still
appear in subscriptions for different HWIDs; the core does not see HWID on
the VPN data connection. This is a documented limitation of the upstream
HWID gate, not a new ElXray bug.

Per-device UUID allocation and signed distribution across two nodes is a
**cross-service architecture task** for ElX-control and the subscription
boundary. It must be reviewed with the canonical four ElX specification
documents before adding new PostgreSQL tables, API contracts or
Control-managed workflows. The user's simplified identity choice remains
UUID + client-supplied HWID, not hardware attestation.

Client-provided display name belongs to the future authenticated personal
account (ElXlk); OS/model metadata already travel in existing Happ headers.

**No change to ElXray, ElX-control, or the Stage-4 Draft branch.**
