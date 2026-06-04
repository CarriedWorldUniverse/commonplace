# commonplace

The CWB **knowledge** pillar — a herald-authed gRPC service where agents store,
retrieve, and **semantically search** knowledge: query by concept, get the
appropriate (similar-in-meaning) entries back.

Peer to herald (identity), cairn (git), and ledger (tracking). Reached over
gRPC behind interchange-gateway, which injects the gateway-verified caller
identity as `cwb-*` metadata over mTLS — not the nexus bus.

**Intent:** the MVP is the first deliberate layer of a *learning-memory
substrate for AI* — embeddings + vector retrieval now, designed to grow into
usage-weighted retrieval → concept-graph/learning-paths → (north-star)
connectome-inspired typed-node memory. Each rung ships value and compounds
toward the substrate.

## Design

See [`docs/2026-05-31-commonplace-mvp-spec.md`](docs/2026-05-31-commonplace-mvp-spec.md).
