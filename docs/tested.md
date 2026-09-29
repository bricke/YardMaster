# Tested with

Tested on a LAN server on 2026-09-29, with Switchyard v0.3.0:

| Provider | Model | How it went |
|---|---|---|
| Anthropic | Claude Haiku 4.5 | Answers well and judges well: 2–4 s per judge decision |
| OpenAI | GPT-6 Luna | Works through both API styles. It reasons at length, so tools need large output limits. Slow as a judge (18 s on a hard prompt) |
| Mistral | Ministral 14B | Fast answers (about 1.3 s) and quick judging (2–3 s), but lenient: at threshold 0.4 it sent a hard design task to the weak model |
| Self-hosted (vLLM) | Qwen 3.8 27B | Works, and thinking can be switched off per model. As a judge it reasoned for minutes, so it's a poor judge |

## What real traffic taught us

The judge advice is also on the Help page.

- **Use a fast judge, not a reasoning model, and make sure it's reliable.** If the judge's
  provider refuses a request, every route that uses that judge fails.
- **Each provider takes different reasoning settings, and the wrong one breaks requests.**
  Mistral's API rejects the vLLM thinking switch, and Claude 4.5 models don't accept `effort`. The
  wizard groups the controls by provider type, but it still offers the thinking switch on hosted
  OpenAI-compatible APIs and `effort` on Claude 4.5. Send a test request after adding a model.
- **Reasoning models spend output tokens on thinking.** With small output limits, their answers
  come back empty.
- **The judge sees only the first and latest user messages**, not tool results. For coding
  agents, a strategy that follows the agent's progress (Auto) or one that judges finished work
  (escalation) may fit better.
