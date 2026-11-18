# Clavis capability/risk matrix (fixture summary)

All remote facts are labelled: `observed` (read-only source), `missing` (not exposed by source), `inferred` (design mapping), or `synthetic` (fixture). Missing data is not guessed.

| Target | Capability | Risk | Default policy |
|---|---|---|---|
| template | parse/render/preview/validate | prompt injection, unbounded context | validate slots; adapter-only conversion; no remote execution |
| MCP server | initialize/tools/cancel | external side effects and credentials | explicit server allowlist; cancellation via context; redact headers |
| AI profile | streaming/structured output/tools/vision/reasoning | provider data disclosure and unsafe output | profile capability declaration; fake HTTP/SSE tests; schema-check before automation |
| agent target | prompt/wait/stop | long-lived execution | cancellation independent of provider; audit summary only |
| account | keychain/env lookup | secret disclosure | store account reference only; never serialize key/header |

Observed remote schema contains catalog slots (`text`, `textarea`, `enum`), release/version/digest and read-only CLI cards. Parser function whitelist, template control flow, template CLI, and provider payload format were `missing` in the inspected workspace.
