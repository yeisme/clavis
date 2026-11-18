# Herdr/MuWeave template compatibility record

Status: `observed` (read-only local inspection through the Herdr-connected workspace; no provider calls).

## Sources and gaps

- Source: `/Users/yeisme/work/muweave-dsh` (`packages/dsh-muweave-core/src/protocol.ts`, `host-safe-catalog.ts`, `packages/muweave-bundle/src/schema.ts`, `packages/muweave-bundle/src/cli.ts`).
- Source: `/Users/yeisme/workspace/pi-local-extensions/extensions/herdr-bridge.ts` (Herdr pane state bridge).
- Missing: no standalone remote template parser/renderer/schema or `render`/`preview`/`validate` template CLI was present in the inspected tree. The catalog exposes metadata and `templates.render_preview` RPC only; its implementation/provider payload is not copied.

## Compatibility matrix

| Area | Observed remote contract | Clavis contract/adapter decision |
|---|---|---|
| Syntax | Catalog URI refs (`promptrepo://...@version?locale=...`), structured slot metadata; no Go-template syntax observed | Keep Clavis `{{name}}`, `{{name=default}}`, `{{name:a|b}}`; third-party adapter maps slots only |
| Variables | `text`, `textarea`, `enum`; enum has options | Map text/textarea to text and enum to select |
| Defaults/required | `required` boolean; no default field observed | Preserve Clavis defaults and required semantics; absent remote default means empty |
| Conditions/loops | Not exposed in observed schema | Unsupported; report missing capability rather than emulate |
| Messages | RPC preview returns `structuredInstruction` and `templateRef` | Clavis keeps provider-neutral system/developer/user messages |
| Function whitelist | Allowlisted CLI cards are read-only and named by operation ID; no template functions observed | No remote functions admitted into core |
| Diagnostics | RPC error `{code,message,retryable?,operationRef?}` | Preserve code/message/retryability; never expose secrets |
| Migration | Version/release/digest are explicit (`1.0.0`, SHA-256 digest) | Adapter accepts version/digest metadata and leaves source template immutable |
| Commands/JSON | `muweave-bundle validate`, `doctor`, `inspect`, `sync`; JSON/agent output envelopes (`ok`, `command`, `error`, reports) | No remote command execution in core; fixture records shape only |

## Safety and provenance

External facts are labelled `observed`; absent parser/renderer/CLI facts are `missing`; the local Clavis compatibility decision is `inferred`; fixture examples are `synthetic`. No API keys, accounts, authorization headers, provider payloads, or hidden prompts are stored.
