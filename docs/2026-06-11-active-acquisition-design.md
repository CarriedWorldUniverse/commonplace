# commonplace: active acquisition — recall-or-acquire with a per-org researcher

**Status:** spec (operator-directed 2026-06-11)
**Driver:** shadow (with the operator)
**Builds on:** `docs/2026-05-31-commonplace-mvp-spec.md` (Store/Search + the KnowledgeService gRPC surface).

## The idea

Today commonplace is a passive store: agents `Store` knowledge and `Search` what's been stored. The README's own framing is bigger — *"the first layer of a learning-memory substrate for AI."* This spec takes the step from *remembers* to *learns*: on a knowledge miss, commonplace can have the knowledge **found**, **returned**, and **stored** so the next ask is a cheap recall — a self-populating knowledge layer.

## The decision (operator 2026-06-11): per-org researcher, not a platform-baked one

commonplace is a **CWB pillar — multi-tenant**. Baking a researcher into the pillar would mean every org shares *one* researcher, *one* cost center, *one* model and its biases. That violates CWB's whole isolation model (herald per-org identity, custodian per-org crypto). So:

> **The researcher is org-configured. Each org plugs in their own "harrow." Their knowledge, their cost, their model.**

commonplace the pillar provides the **substrate + the acquire seam**; it ships **no researcher**. On a miss it *calls* the org's configured research provider. Our single-operator nexus is simply one org that plugs in *our* harrow as its researcher.

## The contract

### `Acquire` (recall-or-acquire) — a new, explicitly-opt-in RPC

A distinct RPC alongside `Search` (NOT a change to `Search` — a fast recall must stay fast and cheap; acquisition is slow and costs money/tokens, so it's opted into, never implicit):

1. **Recall first** — semantic search over the org's stored knowledge (the existing `Search` path).
2. **On miss / low-confidence / stale hit** — invoke the org's **configured researcher** with the query.
3. **Store the result** — write the researcher's findings back into the org's knowledge **with full provenance** (below).
4. **Return** — the answer, flagged with its trust tier and sources.

Caller controls when this fires (mode/threshold params: research-on-miss, refresh-if-stale-than-N, never-research). A plain `Search` never auto-researches.

### Per-org researcher configuration

- Each org registers a **researcher endpoint** in its commonplace config (a research RPC the org implements / a registered agent the org runs). Shape: `Research(query, context) → {findings, sources[], confidence}`.
- commonplace calls it over the platform boundary; the researcher runs in the **org's** trust domain, on the **org's** LLM/search creds (brokered via custodian — no platform-held keys), at the **org's** cost. Our nexus points this at harrow (which already returns structured reviews + can drive lynxai).
- commonplace never embeds research logic — it's a delegating seam. An org with no researcher configured gets a pure store (today's behavior).

### Provenance + trust tier — the non-negotiable

This is what separates "a learning substrate" from "a confidently-wrong cache." Every stored `Entry` gains:

- **`trust_tier`**: `human_authored` (ground truth) · `code_derived` (verifiable) · `llm_acquired` (verify before trusting).
- **`sources[]`** (URLs/refs the researcher cited), **`confidence`**, **`acquired_at`**, **`researcher_id`**.

`Search`/`Acquire` return these so callers always know what they're trusting. **LLM-acquired knowledge is recalled flagged — "here's what was found, from these sources, on this date" — never silently as fact.** Without this, one cached hallucination is recalled with false confidence forever. Stale or low-confidence entries can be re-acquired on read (configurable). This directly answers the *calcification / staleness / prompt-injection-on-read* anti-patterns from the continual-harness work — which bite hardest precisely here.

## Multi-tenancy (inherits CWB's model)

- **Knowledge isolation**: per-org, herald-scoped (org A's facts never reach org B) — the pillar's existing tenancy.
- **Researcher isolation**: per-org config; org A's harrow ≠ org B's.
- **Cost isolation**: the researcher burns the org's own LLM/search budget via the org's brokered creds. The platform never pays for an org's research.

## Architecture

- **commonplace (pillar)**: the `Acquire` RPC + the provenance/trust-tier columns on Entry + the recall→delegate→store-with-provenance loop + per-org researcher-endpoint config. All within the existing gRPC-over-mTLS KnowledgeService.
- **Researcher (external, org-owned)**: implements the `Research` contract; not part of the pillar. Our nexus wires harrow (+ lynxai for web).
- **custodian**: brokers the org's research-credential use (the researcher's LLM/search keys) — no raw secrets in the loop.

## Non-goals (v1)

- commonplace doing the LLM research itself (it delegates — always).
- Auto-research on every `Search` (acquire is explicit/opt-in).
- A platform-default researcher (per-org only; no-researcher = pure store).
- Cross-org knowledge sharing or a shared research cache (isolation stays hard).

## Open questions

- **Researcher invocation**: synchronous RPC (caller waits for research) vs async dispatch (acquire returns "researching", result lands later via a callback/notification). Sync is simpler; async fits long research. Proposed: sync with a timeout + the option to return the stale/partial recall immediately and refresh in the background.
- **Staleness/refresh policy**: per-entry TTL by trust tier (llm_acquired short, human_authored none)? Operator-configurable.
- **Cost caps**: per-org acquire-rate / token caps so a runaway agent can't burn an org's budget.
- **Curation**: does every acquired answer get stored, or only above a confidence threshold? (Proposed: store all with confidence, let recall filter — but prune low-confidence on a schedule.)

## Sequencing

Spec → operator review → plan. Build order: (1) provenance/trust-tier on Entry (the spine — do first, it's useful even without acquisition: human vs code vs llm-authored knowledge); (2) per-org researcher config + the `Acquire` RPC delegating to it; (3) staleness/refresh + cost caps. Our nexus validates the loop with harrow as the first researcher.
