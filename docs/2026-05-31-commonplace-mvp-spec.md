# commonplace — CWB MVP spec (agent knowledge pillar)

**Status:** draft for approval · 2026-05-31
**Goal:** a herald-authed HTTP service behind the gateway where aspects **store, retrieve, and semantically search** knowledge — query by concept, get the *appropriate* (similar-in-meaning) entries back — so the nexus team's knowledge lives on CWB instead of the ad-hoc nexus knowledge store.
**Why now:** commonplace is the **knowledge** leg of the CWB MVP agent loop (auth+git+issues+knowledge — see `cwb-conformance/docs/2026-05-31-cwb-mvp-definition.md`), a first-class pillar peer to herald/cairn/ledger.

**Intent (load-bearing — not just "ship a knowledge store"):** this MVP is the **first deliberate layer of a learning-memory substrate for AI** (`project_commonplace_neural_vision`). Today's AI memory is RAG — flat chunks re-retrieved by similarity, no accumulation, no structure that learns. The strategic thesis: an agent memory that grows/structures/adapts is a genuine differentiator. So the MVP's data model, embeddings-as-first-class-data, and retrieval seam are designed as the **foundation that thesis grows from** — built to climb the ladder (embeddings → usage-feedback weighting → concept-graph/learning-paths → connectome-inspired typed nodes), never betting the product on the unproven end. Same MVP build; intentional foundation.

---

## 1. The one-paragraph architecture

A single Go binary (`cmd/commonplace`) serving a herald-authed HTTP/REST API behind interchange-gateway, org-scoped, decoupled from the nexus WS-bus (`project_cwb_http_native`). It graduates the nexus knowledge store's model (`{topic, content, owner, scope}`, FTS5) but its core is **neural semantic retrieval**: on store, an entry is **embedded** (vector) via an AI-switchable embedding model through the existing provider seam; on query, the query is embedded and matched by **vector nearest-neighbour**, **fused with FTS5 keyword** (hybrid) for both concept and exact-term recall. SQLite holds content + metadata; `sqlite-vec` holds the vectors — same SQLite-everywhere pattern as the other pillars, no new DB infra. Embeddings + entry relationships are stored as **first-class data** behind a clean `search` seam, so the future learning-memory layers (usage-weighted retrieval, concept graph, typed-node structures) can grow over the same data without re-architecting.

```
  aspect ──herald token──► gateway ──mTLS, X-CWB-*──► cmd/commonplace
                                                       store: embed(content) → SQLite + sqlite-vec
                                                       search(q): embed(q) → vector NN ⊕ FTS5 → ranked
                                                       embed model via AI seam (switchable; local default)
```

---

## 2. Scope — what's IN

1. **Knowledge entries.** `{id, org, owner (agent id), topic, content, visibility (private|org), tags[], embedding, created/updated}`. SQLite for content+metadata; `sqlite-vec` for vectors. Owner from the herald identity; org-scoped; `visibility` = the nexus store's own/shared notion (private to the agent vs shared across the org).
2. **Neural semantic retrieval (the core).** Embed on store; on `search`, embed the query → **vector nearest-neighbour** over the org's entries, **fused with FTS5 keyword** (hybrid ranking). Concept queries *and* exact-term queries both work; "similar/appropriate knowledge surfaces."
3. **AI-switchable embedding model.** Embeddings go through the existing provider seam (per the model-routing policy / NEX-188 frame). **MVP default: a local model (ollama embeddings)** — private, free, no API cost, BYOAI-consistent; swappable to an API embedder later. The model is config, not hardcoded.
4. **Verbs (REST, herald-authed, gateway-fronted):**
   - `POST /api/knowledge` — store (embeds on write); owner = `X-CWB-Subject`.
   - `GET /api/knowledge/search?q=...` — hybrid semantic+keyword retrieval; org-scoped; own + org-shared visibility; ranked with scores.
   - `GET /api/knowledge/{id}` — retrieve one.
   - `GET /api/knowledge` — list (own + org-shared).
   - `PATCH/DELETE /api/knowledge/{id}` — update (re-embeds) / delete.
5. **Auth/transport.** Behind the gateway; trust the mTLS-injected `X-CWB-*` (`sub`→owner, `org`→scope, `scope`→permission `knowledge:read`/`write`); HTTP-native, not the WS-bus; TLS everywhere (`project_cwb_tls_everywhere`).
6. **Deploy as a CWB product.** Containerfile + k3s manifests in `cwb` ns; ClusterIP behind the gateway (mTLS); SQLite+sqlite-vec on a PVC; embedding model reached via the AI seam; gateway route `/knowledge`. Mirrors herald/ledger.
7. **Substrate-ready data.** Embeddings + (future) entry relationships stored as first-class data behind a stable `search` seam — the deliberate foundation for the learning-memory ladder (§7).

---

## 3. Data model

```
entry
  id          text PK
  org         text                 -- herald org (tenant scope)
  owner       text                 -- herald agent id (actor)
  topic       text
  content     text
  visibility  text                 -- 'private' | 'org'
  tags        text                 -- JSON array
  created_at, updated_at

entry_vec (sqlite-vec)             -- vector index over entry embeddings
  entry_id    → entry.id
  embedding   float[N]             -- N = the embedding model's dim

entry_fts (FTS5)                   -- keyword index over topic+content

-- (future, NOT MVP) entry_edge(from_id, to_id, kind, weight) — the substrate
-- for concept-graph / learning-paths; the schema leaves room, the MVP doesn't build it.
```

Retrieval fuses `entry_vec` (semantic) + `entry_fts` (keyword) over the caller's org + visibility scope. The commented `entry_edge` is named to mark the growth path, not built.

---

## 4. Auth + identity

Same model as ledger: behind the gateway, trust mTLS-injected `X-CWB-*`. `Subject`→owner (per-agent attribution), `Org`→tenant scope (single-org MVP, mechanism present), `Scopes`→`knowledge:read`/`knowledge:write`. ClusterIP-locked; reachable only over the mTLS gateway hop.

---

## 5. Dependencies

- **herald** (live) + **interchange-gateway** (live) + **mTLS mesh** (platform) — identity + transport.
- **An embedding model via the AI provider seam** — local (ollama) default; the provider routing already exists in the runtime. This is the one new runtime dependency; it's config-switchable.
- **`sqlite-vec`** — SQLite vector extension (vendored/loaded), keeping the SQLite-everywhere pattern.
- No cairn/ledger dependency — commonplace stands alone in the agent loop.

---

## 6. Build sequence (for the implementation plan)

1. **Spec sign-off** (this doc).
2. **`cmd/commonplace` server** + SQLite schema (entry + FTS5) + Containerfile; build-green.
3. **Embedding seam** — `Embed(text) → []float32` via the AI provider (local default); config-switchable; `sqlite-vec` wired.
4. **Store** — `POST /api/knowledge`: persist + embed + index (FTS5 + vec); owner from `X-CWB-Subject`.
5. **Hybrid search** — `GET /api/knowledge/search`: embed query → vec NN ⊕ FTS5, fused ranking, org+visibility scoped.
6. **Retrieve/list/update/delete** + re-embed on update.
7. **Gateway-identity middleware** (shared pattern with ledger) — trust `X-CWB-*` over mTLS.
8. **k3s deploy** — manifests, gateway route, PVC, embedding-model reachability.
9. **cwb-conformance commonplace layer** — store an entry, then a *differently-worded conceptual* query surfaces it; org-scope + visibility isolation.

**DoD:** an aspect, herald-authed through the gateway, stores a knowledge entry, then a conceptually-related query (different wording) **surfaces it via semantic search** — org-scoped, owner-tagged — and the conformance commonplace layer + journey exercise it green.

---

## 7. Out of MVP — the learning-memory ladder + human layer

This MVP is rung 1. Sequenced after (`project_commonplace_neural_vision`):
- **Usage-feedback weighting** — retrieval starts to *learn* which results were appropriate (re-rank from feedback). The first "learning" step.
- **Concept graph / learning-paths** — `entry_edge` activated; retrieval surfaces *paths* through related knowledge, not just nearest neighbours; structure adapts with use.
- **Connectome-inspired typed-node memory** (north-star, original research) — neuron-type taxonomy → digital node-types as the storage substrate. Steer toward it; don't build the product on it.
- **Rich human wiki/docs UI** — the human-facing surface (post-MVP human layer, on the cut line with cairn/ledger UIs + path-A).
- **Cross-org knowledge**, **migration of the WS-bus knowledge store** (it stays for nexus-internal until then).

Each rung ships value AND compounds toward the substrate; never bet the product on the unproven end.

---

## 8. Open questions for the plan (small, non-blocking)

- **Embedding model + dim** — which local ollama model (e.g. `nomic-embed-text`, dim 768) for MVP; confirm the AI-seam call shape + that dim is fixed per-deployment (re-embed on model change).
- **Hybrid fusion** — reciprocal-rank-fusion vs weighted score blend for vec⊕FTS5; pin in the plan.
- **`sqlite-vec` vs brute-force cosine** — at team-scale corpus, brute-force cosine over stored vectors may suffice; `sqlite-vec` is cleaner + scales. Pin in the plan.
- **Exact `knowledge:*` scope strings** — cross-pillar, pin in the plan.
- **Re-embed on update** — sync (in request) vs async; MVP can be sync given small scale.
