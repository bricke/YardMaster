# Switchyard Reference (for YardMaster)

What YardMaster relies on from Switchyard. Captured from the upstream repo at commit `a601a9a`
(2026-09-28, release line **v0.3.0**, pre-1.0). **Check against the current upstream docs before
relying on any detail**, because the API is still changing.

Upstream: https://github.com/NVIDIA-NeMo/Switchyard (Apache-2.0)
Key upstream docs: `crates/switchyard-server/README.md`, `docs/reference/toml_schema.md`,
`docs/cli_reference.md`, `docs/routing_algorithms/overview.md`.

## What Switchyard is

A Rust proxy and library that picks which LLM serves each request. Clients call a *route* as if it
were a model, and Switchyard selects a *target* model and translates between OpenAI Chat, OpenAI
Responses and Anthropic Messages formats (streaming included). It retries 408/429/5xx and
connection errors, then falls back through the route's ordered targets. Request content is never
shared between requests: each request carries its own conversation. The one cross-request state is
**routing affinity**: an `llm_classifier` with `classify_trigger = "user_turn"` or `"new_session"`
reuses its target choice for a session (keyed on `x-switchyard-session-id`, or on a hash of the
first user message with `message_hash_fallback`). It shares a *target choice*, never content.

## CLI: `switchyard-server`

| Option | Default | Notes |
|---|---|---|
| `--config PATH` | required | Native TOML deployment |
| `--host` | `0.0.0.0` | |
| `-p, --port` | `4000` | |
| `--dry-run` | off | Validate the config without binding. **YardMaster's validator** |
| `--routing-log-file PATH` | none | JSONL per-request routing log. Needed for session stats |
| `--shutdown-timeout` | `30s` | Drain window on shutdown |
| `--tls-cert` / `--tls-key` | none | |

Install: `cargo install --locked switchyard-server`.

**Distribution (checked 2026-09-28, v0.3.0):** releases go to PyPI (`nemo-switchyard` wheels),
crates.io (`switchyard-server` among others) and GitHub Releases. **No Docker image is published.**
The repo's `Dockerfile` builds from source (`rust:1.96.1-bookworm` → `debian:bookworm-slim`, UID
1000, port 4000). `.cargo/config.toml` sets `target-cpu=x86-64-v3` (AVX2, Haswell or newer) and
`target-cpu=neoverse-n1` on aarch64, so binaries built with it won't run on older CPUs.

**Routing log:** `--routing-log-file` appends one JSON record per completed routed response
(streams after they drain). Fields (v0.3.0 `routing_log.rs`): `ts`, `route_id`, `algorithm`,
`origin` (from `x-switchyard-origin`), `task` (`x-switchyard-intake-task`), `trial_id`
(`x-switchyard-trial-id`, only logged, so YardMaster uses it as its request ID), `session_id`,
`model`, `tier`, `prompt_tokens` (includes cached and cache-creation), `cached_tokens`,
`cache_creation_tokens`, `completion_tokens`, `reasoning_tokens`, `total_tokens`. Classifier and
judge calls get their own records with `tier = "classifier"`; the served call's `tier` is always
empty. The file is opened once in append mode and never reopened.

## HTTP endpoints

| Method | Path | Use in YardMaster |
|---|---|---|
| `GET` | `/health` | Liveness indicator |
| `GET` | `/v1/models` | List configured routes |
| `GET` | `/v1/stats` | Per-model usage and algorithm stats (monitoring) |
| `POST` | `/v1/stats/reset` | "Reset counters" button |
| `GET` | `/metrics` | Prometheus text (monitoring) |
| `GET` | `/v1/routing/session-stats?session_id=ID` | Per-session totals (only with `--routing-log-file`) |
| `POST` | `/v1/decision` | Playground: `{"input_format":"openai_chat","request":{...}}` returns the selected target and fallbacks without the answer call. Classifier and judge calls still run and cost tokens |
| `POST` | `/v1/chat/completions`, `/v1/messages`, `/v1/responses` | Proxied traffic (not used by the UI) |

Session headers: `x-switchyard-session-id`, and `x-switchyard-origin` (client label that shows up in
routing records).

## Key Prometheus metrics

`switchyard_requests_total{model}`, `switchyard_errors_total{model}`,
`switchyard_prompt_tokens_total{model}`, `switchyard_completion_tokens_total{model}`,
`switchyard_cached_tokens_total{model}`, `switchyard_cache_creation_tokens_total{model}`,
`switchyard_total_latency_ms{model}` (histogram), `switchyard_routing_overhead_ms{algorithm}`,
`switchyard_llm_calls_total{algorithm,selected_model,outcome}`,
`switchyard_upstream_attempts_total{outcome,code}`, `switchyard_router_retry_recovered_total`,
`switchyard_classifier_fail_open_total{judge_model,reason}`. Full table in the server README.

## Deployment TOML (three layers)

```toml
schema_version = 1

[llm_clients.openrouter]          # how to reach a provider
format = "openai_chat"            # openai_chat | openai_responses | anthropic_messages
base_url = "https://openrouter.ai/api/v1"
api_key_env = "OPENROUTER_API_KEY" # names an env var; the secret is never in the TOML
# forward_auth = true              # alternative: pass the caller's credentials through
# max_retries = 2

[targets.strong]                  # a model on a client
id = "openai/gpt-4o"
llm_client = "openrouter"

[targets.weak]
id = "openai/gpt-4o-mini"
llm_client = "openrouter"

[routes.smart]                    # what clients call; decides which target serves
id = "smart"
type = "random"
targets = ["strong", "weak"]
weights = [3, 7]
```

Route `type`s: `auto` (preset: stage router, efficient model first), `llm_classifier`
(modes: capability / escalation / custom), `stage_router`, `composite`, `plan_execute`, `advisor`,
`random`, `passthrough`. `prefill_router` is experimental. Sub-agent routing is set with
`subagents` on some types. Target-level options include `system_prompt` and `extra_body`.

## Known gaps YardMaster has to work around

- **No hot reload** (confirmed against v0.3.0 source and docs, 2026-09-28). Applying config means
  restarting the server.
- **No inbound caller auth.** Anyone who reaches the port can use the configured provider keys.
- **Caller credentials can be forwarded.** A client with `forward_auth = true` sends the caller's
  `authorization` (and `x-api-key` for Anthropic) upstream, and `fallback_client` forwards all
  end-to-end headers. A gateway in front must strip its own credentials before forwarding.
- **No cost data.** PR #378's `/v1/savings` was not merged, so YardMaster computes cost from token
  metrics with its own price table.
- `switchyard-server` is labeled **demo** stability upstream; `libsy` is beta.
