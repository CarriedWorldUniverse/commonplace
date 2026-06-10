# commonplace MVP — Implementation Plan

> **⚠️ HISTORICAL — this plan was executed and commonplace has since migrated to gRPC-only over mTLS.** The `net/http` REST surface described below is no longer the live shape: commonplace serves a `KnowledgeService` (Store / Search) over gRPC, and any HTTP/JSON view is synthesized at interchange's gateway edge. Read this for the *design intent + decisions*, not the transport. To ship anything new (e.g. recall synthesis), implement it as a `KnowledgeService` **RPC** in `grpcserver.go`, not a `net/http` handler.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build commonplace — the CWB knowledge pillar: a herald-authed HTTP service where aspects store/retrieve/semantically-search knowledge (query by concept, appropriate entries surface). The neural-semantic foundation (embeddings + vector + hybrid keyword) — rung 1 of a learning-memory substrate, built to grow toward learning-paths but not implementing them.

**Architecture:** A single Go binary (cmd/commonplace) serving a herald-authed REST API behind interchange-gateway. On store, entries are embedded (AI-switchable model, local ollama default) and indexed in SQLite (content+metadata+FTS5) + sqlite-vec (vectors). Search embeds the query and fuses vector nearest-neighbour with FTS5 keyword. Identity from the gateway's mTLS-trusted X-CWB-*. HTTP/REST, not the WS-bus. Embeddings + relationships stored as first-class data behind a clean search seam so the learning-memory ladder can grow over the same data.

**Tech Stack:** Go 1.26, modernc.org/sqlite (+ sqlite-vec), the AI provider seam for embeddings (ollama default), herald (live) + interchange-gateway (live) for identity, mTLS mesh.

---

## Resolved decisions (read before starting)

These resolve the spec's §8 open questions and the riskiest infra choice. They are load-bearing for the whole build — do not re-litigate them mid-implementation.

### D1. SQLite driver + vector storage — **pure-Go, no cgo, brute-force cosine for MVP**

The "Tech Stack" line above names `modernc.org/sqlite`, but that pure-Go driver does **not** support loadable C extensions, so it cannot load upstream `sqlite-vec` (the `vec0` virtual table). Rather than introduce cgo (`mattn/go-sqlite3`) — which would break the CGO_ENABLED=0 / scratch-image pattern every other CWB pillar uses — commonplace uses **`github.com/ncruces/go-sqlite3`**, the same pure-Go (WASM) SQLite driver **ledger already ships** (`ledger/go.mod` → `github.com/ncruces/go-sqlite3 v0.34.1`). It:

- builds with `CGO_ENABLED=0` (WASM-backed, no cgo),
- supports FTS5 out of the box,
- ships a pure-Go vector extension at `github.com/ncruces/go-sqlite3/ext/vec1` (registered via `driver.Open(dsn, vec1.Register)`), exposing `CREATE VIRTUAL TABLE … USING vec1` + `vec1_from_json(...)`. NOTE: this is the driver's **`vec1`** port, NOT upstream sqlite-vec's `vec0` — the SQL surface differs (`vec1_from_json`, `vec1_config`), so don't copy sqlite-vec `vec0` snippets verbatim.

**MVP vector path = brute-force cosine over stored `[]float32` BLOBs**, NOT the `vec1` virtual table. Rationale (matches spec §8 "`sqlite-vec` vs brute-force cosine"):

- Team-scale corpus (hundreds–low-thousands of entries per org) → a full scan of stored vectors is microseconds-to-milliseconds; an ANN index earns nothing yet.
- Vectors stay as a plain `entry_vec.embedding BLOB` (little-endian float32) on the `entry` row's sibling table — trivially testable with a fake embedder, no virtual-table lifecycle, no WASM-extension registration in the hot path.
- The `search` seam (`Searcher` interface, below) is the abstraction boundary: swapping brute-force cosine for `ext/vec1` (or a future ANN store) is a **single implementation swap behind the seam**, zero handler/API change. This is the deliberate growth seam the spec asks for.

**Tradeoff stated:** brute-force cosine is O(n·dim) per query and loads all org vectors into memory per search. Acceptable and correct at MVP scale; the seam exists precisely so the `vec1` virtual table drops in when corpus size justifies it. The plan wires `ext/vec1` registration as a **named, commented growth hook** (Task 2 step), not a live dependency.

Where the spec/header says "sqlite-vec", read it as **"the vector layer"**, implemented for MVP as brute-force cosine over `[]float32` blobs via the ncruces pure-Go driver, with `ext/vec1` as the named scale-up.

### D2. Embedding model — **ollama `nomic-embed-text`, dim 768, config-switchable**

Mirrors `nexus/runtime/providers/ollama-local/ollama.go` exactly: POST `{base}/api/embeddings` with `{"model":..., "prompt":...}`, response `{"embedding":[...float32]}`. Defaults locked to `nomic-embed-text` / dim 768 (operator #7676 lineage). Config via env:

- `COMMONPLACE_EMBED_PROVIDER` (default `ollama`)
- `COMMONPLACE_EMBED_URL` (default `http://localhost:11434`)
- `COMMONPLACE_EMBED_MODEL` (default `nomic-embed-text`)
- `COMMONPLACE_EMBED_DIM` (default `768`)

Dim is **fixed per-deployment**: changing the model is a one-way door requiring a full re-embed (named in "Future", not automated in MVP). A **fake/stub embedder** (deterministic, hash-seeded) makes Tasks 3–5 testable with no live model.

### D3. Hybrid fusion — **Reciprocal Rank Fusion (RRF), k=60**

Pinned to RRF over weighted-score blending: RRF needs no per-source score normalization (vector cosine ∈ [-1,1] vs FTS5 bm25 are not comparable raw), is parameter-light, and is the standard hybrid-retrieval fusion. `score = Σ_sources 1/(k + rank)` with `k=60` (the canonical default). Each entry's fused score is the sum of its RRF contributions from the vector ranking and the FTS5 ranking; entries appearing in both rank higher. Pinned constant `rrfK = 60`.

### D4. Scope strings — **`knowledge:read` / `knowledge:write`**

From `X-CWB-Scopes` (space-joined, per `interchange/internal/gateway/gateway.go` `injectIdentity`). `knowledge:read` gates GET search/get/list; `knowledge:write` gates POST/PATCH/DELETE. Cross-pillar-consistent with ledger's `repo:*`/`issue:*` shape.

### D5. Re-embed on update — **synchronous, in-request**

PATCH that changes `content` (or `topic`) re-embeds inline before responding, at MVP scale (one embed call, sub-second). Async re-embed is named in "Future", not built.

### D6. Auth model — **gateway-trusted X-CWB-* only (no local JWT verify)**

commonplace sits behind interchange-gateway on a ClusterIP; the gateway has already verified the herald token and injected `X-CWB-{Subject,Org,Kind,Scopes,Responsible-Human}` (stripping any client-supplied copies — see `gateway.go` `trustedHeaders` + `injectIdentity`). commonplace **trusts these headers** and does NOT re-verify a JWT (ledger's `auth.go` JWT path is its in-process/standalone mode; the CWB-fronted mode is header-trust). A `COMMONPLACE_TRUST_HEADERS=false` escape hatch is out of MVP scope; the service always trusts the gateway hop. ClusterIP-locked deploy (Task 7) is what makes header-trust safe.

---

## Repo + module conventions

- New repo at `/Users/jacinta/Source/commonplace` (git already initialized; `docs/` + `README.md` present).
- Module path: `github.com/CarriedWorldUniverse/commonplace`.
- Go package layout:
  - `cmd/commonplace/main.go` — binary entrypoint (env wiring, mux, ListenAndServe).
  - root package `commonplace` (files at repo root, mirroring ledger's flat-package style: `service.go`, `schema.go`, `store.go`, `search.go`, `embed.go`, `identity.go`, `handlers.go` + `*_test.go`). Flat root package keeps it consistent with ledger and avoids premature internal/ structure.
- Commit prefix: `commonplace-mvp:` (no dedicated NEX key; tracked under the CWB MVP umbrella). Do NOT push or open PRs — the operator handles git remotes.
- Every task ends build-green (`go build ./...`) and test-green (`go test ./...`) before its commit.

---

## Task 1 — `cmd/commonplace` server + SQLite schema (entry + FTS5) + Containerfile; build-green

Stand up the module, the SQLite open + schema (entry + entry_fts, with entry_vec and the commented entry_edge), a healthz mux, and a build-green Containerfile. No embedding, no vectors-populated, no handlers beyond healthz yet.

- [ ] **1.1 — Init module.** Run:
  ```
  cd /Users/jacinta/Source/commonplace && go mod init github.com/CarriedWorldUniverse/commonplace && printf 'go 1.26\n' >/dev/null
  ```
  Then set the Go version line. Expect: `go mod init` prints `go: creating new go.mod: module github.com/CarriedWorldUniverse/commonplace`. Edit `go.mod` so the `go` directive reads `go 1.26.0`. Verify: `go version` prints a 1.26.x toolchain.

- [ ] **1.2 — Add the ncruces driver dep (failing-compile anchor).** Create `/Users/jacinta/Source/commonplace/service.go`:
  ```go
  // Package commonplace is the CWB knowledge pillar: a herald-authed HTTP
  // service where aspects store, retrieve, and semantically search
  // knowledge. SQLite (pure-Go, ncruces driver) holds content+metadata
  // +FTS5; a sibling vector table holds embeddings for hybrid retrieval.
  package commonplace

  import (
  	"context"
  	"database/sql"
  	"fmt"

  	"github.com/ncruces/go-sqlite3/driver"
  	_ "github.com/ncruces/go-sqlite3/embed" // pure-Go WASM SQLite runtime
  )

  // Config carries the service's runtime configuration.
  type Config struct {
  	// DBPath is the on-disk location of commonplace.db (":memory:" ok for tests).
  	DBPath string
  	// Embedder produces vectors for store + search. Required.
  	Embedder Embedder
  }

  // Service is the commonplace knowledge service.
  type Service struct {
  	cfg      Config
  	db       *sql.DB
  	embedder Embedder
  }

  // New opens (or creates) commonplace.db, applies the embedded schema,
  // and returns a ready Service. applySchema is idempotent.
  func New(ctx context.Context, cfg Config) (*Service, error) {
  	if cfg.DBPath == "" {
  		return nil, fmt.Errorf("commonplace.New: DBPath required")
  	}
  	if cfg.Embedder == nil {
  		return nil, fmt.Errorf("commonplace.New: Embedder required")
  	}
  	dsn := "file:" + cfg.DBPath + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)"
  	db, err := driver.Open(dsn)
  	if err != nil {
  		return nil, fmt.Errorf("commonplace.New: open %s: %w", cfg.DBPath, err)
  	}
  	if err := db.PingContext(ctx); err != nil {
  		_ = db.Close()
  		return nil, fmt.Errorf("commonplace.New: ping: %w", err)
  	}
  	if err := applySchema(ctx, db); err != nil {
  		_ = db.Close()
  		return nil, err
  	}
  	return &Service{cfg: cfg, db: db, embedder: cfg.Embedder}, nil
  }

  // Close releases the DB handle.
  func (s *Service) Close() error {
  	if s.db != nil {
  		return s.db.Close()
  	}
  	return nil
  }
  ```
  NOTE: `driver.Open(dsn)` (no extension hook) is the MVP form — brute-force cosine needs no `vec1` registration. The `vec1` growth hook is added as a comment in Task 2. This references `Embedder` (Task 2) and `applySchema` (next step) which don't exist yet → won't compile yet; that's expected, the next two steps complete the unit.

- [ ] **1.3 — Schema file.** Create `/Users/jacinta/Source/commonplace/schema.sql`:
  ```sql
  -- commonplace schema. Idempotent (IF NOT EXISTS throughout).

  CREATE TABLE IF NOT EXISTS entry (
      id          TEXT PRIMARY KEY,         -- ULID-ish; opaque id minted on store
      org         TEXT NOT NULL,            -- herald org (tenant scope)
      owner       TEXT NOT NULL,            -- herald agent/subject id (actor)
      topic       TEXT NOT NULL,
      content     TEXT NOT NULL,
      visibility  TEXT NOT NULL DEFAULT 'private', -- 'private' | 'org'
      tags        TEXT NOT NULL DEFAULT '[]',      -- JSON array of strings
      created_at  TEXT NOT NULL,            -- RFC3339
      updated_at  TEXT NOT NULL
  );

  CREATE INDEX IF NOT EXISTS entry_org_idx ON entry(org);
  CREATE INDEX IF NOT EXISTS entry_org_owner_idx ON entry(org, owner);

  -- Vector store. MVP: plain float32-BLOB column, brute-force cosine at
  -- search time (see plan D1). embedding is little-endian float32[dim].
  -- dim is fixed per-deployment by the embedding model.
  CREATE TABLE IF NOT EXISTS entry_vec (
      entry_id   TEXT PRIMARY KEY REFERENCES entry(id) ON DELETE CASCADE,
      dim        INTEGER NOT NULL,
      embedding  BLOB NOT NULL
  );

  -- FTS5 keyword index over topic+content. Contentless-external: we keep
  -- entry as the source of truth and mirror into FTS on write.
  CREATE VIRTUAL TABLE IF NOT EXISTS entry_fts USING fts5(
      topic,
      content,
      entry_id UNINDEXED
  );

  -- (future, NOT MVP) the substrate for concept-graph / learning-paths.
  -- The schema NAMES the growth path; the MVP does not build it.
  -- CREATE TABLE IF NOT EXISTS entry_edge (
  --     from_id  TEXT NOT NULL REFERENCES entry(id) ON DELETE CASCADE,
  --     to_id    TEXT NOT NULL REFERENCES entry(id) ON DELETE CASCADE,
  --     kind     TEXT NOT NULL,   -- relationship type (typed-node future)
  --     weight   REAL NOT NULL DEFAULT 1.0,
  --     PRIMARY KEY (from_id, to_id, kind)
  -- );
  ```

- [ ] **1.4 — applySchema.** Create `/Users/jacinta/Source/commonplace/schema.go`:
  ```go
  package commonplace

  import (
  	"context"
  	"database/sql"
  	_ "embed"
  	"fmt"
  	"strings"
  )

  //go:embed schema.sql
  var schemaSQL string

  // applySchema runs the embedded DDL statement-by-statement. Idempotent:
  // every statement is IF NOT EXISTS, so re-running on an existing DB is a
  // no-op. Splitting on ';' is safe because schema.sql contains no triggers
  // or semicolon-bearing string literals.
  func applySchema(ctx context.Context, db *sql.DB) error {
  	for _, raw := range strings.Split(schemaSQL, ";") {
  		stmt := strings.TrimSpace(stripSQLComments(raw))
  		if stmt == "" {
  			continue
  		}
  		if _, err := db.ExecContext(ctx, stmt); err != nil {
  			return fmt.Errorf("commonplace.applySchema: %s: %w", firstLine(stmt), err)
  		}
  	}
  	return nil
  }

  // stripSQLComments removes line-level `--` comments. The commented-out
  // entry_edge block must not reach the DB as a statement.
  func stripSQLComments(s string) string {
  	var b strings.Builder
  	for _, line := range strings.Split(s, "\n") {
  		if i := strings.Index(line, "--"); i >= 0 {
  			line = line[:i]
  		}
  		b.WriteString(line)
  		b.WriteString("\n")
  	}
  	return b.String()
  }

  func firstLine(s string) string {
  	if i := strings.IndexByte(s, '\n'); i >= 0 {
  		return s[:i]
  	}
  	return s
  }
  ```

- [ ] **1.5 — Embedder placeholder so the package compiles standalone is NOT done here** — `Embedder` is introduced in Task 2. To keep Task 1 build-green on its own, add a minimal interface stub now in a new file `/Users/jacinta/Source/commonplace/embed.go` containing ONLY the interface (the real ollama + fake impls land in Task 2):
  ```go
  package commonplace

  import "context"

  // Embedder turns text into a fixed-length vector. The store path embeds
  // content on write; search embeds the query. This is the AI-switchable
  // seam (plan D2) — ollama-backed in production, faked in tests.
  type Embedder interface {
  	Embed(ctx context.Context, text string) ([]float32, error)
  	// Dim is the fixed vector length this embedder produces. Used to
  	// validate stored vectors and size the brute-force cosine scan.
  	Dim() int
  }
  ```

- [ ] **1.6 — Healthz + main.** Create `/Users/jacinta/Source/commonplace/handlers.go`:
  ```go
  package commonplace

  import (
  	"net/http"
  )

  // Handler returns the service's HTTP handler. Task 1 wires only healthz;
  // store/search/CRUD routes are added in Tasks 3–6.
  func (s *Service) Handler() http.Handler {
  	mux := http.NewServeMux()
  	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
  		w.Header().Set("Content-Type", "application/json")
  		_, _ = w.Write([]byte(`{"status":"ok","service":"commonplace"}`))
  	})
  	return mux
  }
  ```
  Create `/Users/jacinta/Source/commonplace/cmd/commonplace/main.go`:
  ```go
  // Command commonplace is the CWB knowledge pillar HTTP service. It runs
  // behind interchange-gateway on a ClusterIP and trusts the mTLS-injected
  // X-CWB-* identity headers (see plan D6).
  //
  // Config (env):
  //   COMMONPLACE_ADDR            listen address (default :8101)
  //   COMMONPLACE_DB              sqlite path (default /var/lib/cwb/commonplace.db)
  //   COMMONPLACE_EMBED_PROVIDER  embedding provider (default "ollama")
  //   COMMONPLACE_EMBED_URL       ollama base URL (default http://localhost:11434)
  //   COMMONPLACE_EMBED_MODEL     embedding model (default nomic-embed-text)
  //   COMMONPLACE_EMBED_DIM       embedding dim (default 768)
  package main

  import (
  	"context"
  	"log"
  	"net/http"
  	"os"
  	"strconv"

  	"github.com/CarriedWorldUniverse/commonplace"
  )

  func main() {
  	addr := env("COMMONPLACE_ADDR", ":8101")
  	dbPath := env("COMMONPLACE_DB", "/var/lib/cwb/commonplace.db")

  	embedder, err := commonplace.NewOllamaEmbedder(commonplace.OllamaConfig{
  		URL:   env("COMMONPLACE_EMBED_URL", "http://localhost:11434"),
  		Model: env("COMMONPLACE_EMBED_MODEL", "nomic-embed-text"),
  		Dim:   envInt("COMMONPLACE_EMBED_DIM", 768),
  	})
  	if err != nil {
  		log.Fatalf("commonplace: embedder: %v", err)
  	}

  	svc, err := commonplace.New(context.Background(), commonplace.Config{
  		DBPath:   dbPath,
  		Embedder: embedder,
  	})
  	if err != nil {
  		log.Fatalf("commonplace: %v", err)
  	}
  	defer svc.Close()

  	log.Printf("commonplace listening on %s (db=%s)", addr, dbPath)
  	if err := http.ListenAndServe(addr, svc.Handler()); err != nil {
  		log.Fatalf("commonplace: %v", err)
  	}
  }

  func env(key, def string) string {
  	if v := os.Getenv(key); v != "" {
  		return v
  	}
  	return def
  }

  func envInt(key string, def int) int {
  	if v := os.Getenv(key); v != "" {
  		if n, err := strconv.Atoi(v); err == nil {
  			return n
  		}
  	}
  	return def
  }
  ```
  NOTE: `main.go` references `commonplace.NewOllamaEmbedder` / `OllamaConfig` which land in Task 2 → the **binary** won't build until Task 2. The **library** (`go build .`) builds now. This is the intended task boundary; do NOT stub a fake ollama in main to force it green — Task 2 completes it.

- [ ] **1.7 — Schema test (TDD).** Create `/Users/jacinta/Source/commonplace/schema_test.go`:
  ```go
  package commonplace

  import (
  	"context"
  	"testing"
  )

  // fakeEmbedder is the deterministic test embedder. Defined in
  // embed_test.go in Task 2; for Task 1 we declare a local minimal one.
  type schemaTestEmbedder struct{}

  func (schemaTestEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
  	return make([]float32, 8), nil
  }
  func (schemaTestEmbedder) Dim() int { return 8 }

  func TestNewAppliesSchemaIdempotently(t *testing.T) {
  	ctx := context.Background()
  	cfg := Config{DBPath: ":memory:", Embedder: schemaTestEmbedder{}}
  	svc, err := New(ctx, cfg)
  	if err != nil {
  		t.Fatalf("New: %v", err)
  	}
  	defer svc.Close()

  	// Re-applying schema on the live DB must be a no-op (idempotent).
  	if err := applySchema(ctx, svc.db); err != nil {
  		t.Fatalf("applySchema second run: %v", err)
  	}

  	// entry, entry_vec, entry_fts must all exist.
  	for _, tbl := range []string{"entry", "entry_vec", "entry_fts"} {
  		var name string
  		err := svc.db.QueryRowContext(ctx,
  			`SELECT name FROM sqlite_master WHERE name = ?`, tbl).Scan(&name)
  		if err != nil {
  			t.Fatalf("expected table %q to exist: %v", tbl, err)
  		}
  	}
  }
  ```
  Run `go test . -run TestNewAppliesSchema`. Expect: a failure first IF `New`/`applySchema` aren't wired — but with 1.2–1.6 complete it should compile and PASS. Expected output prefix: `ok  	github.com/CarriedWorldUniverse/commonplace`.

- [ ] **1.8 — Tidy + library build-green.** Run:
  ```
  cd /Users/jacinta/Source/commonplace && go mod tidy && go build . && go vet .
  ```
  Expect: `go mod tidy` pulls `github.com/ncruces/go-sqlite3` into `go.mod`/`go.sum`; `go build .` exits 0 (library); `go vet .` clean. (`go build ./...` will still fail on `cmd/commonplace` until Task 2 — that's expected.)

- [ ] **1.9 — Containerfile.** Create `/Users/jacinta/Source/commonplace/cmd/commonplace/Containerfile` (mirrors herald's, CGO disabled, scratch base — ncruces is pure-Go so scratch works):
  ```dockerfile
  # commonplace container — pure-Go static binary on scratch.
  # Build from the commonplace repo root:
  #   podman build -f cmd/commonplace/Containerfile -t commonplace:dev .
  #   podman save commonplace:dev | sudo k3s ctr images import -
  #
  # Runtime config via env (see cmd/commonplace/main.go):
  #   COMMONPLACE_ADDR, COMMONPLACE_DB, COMMONPLACE_EMBED_{PROVIDER,URL,MODEL,DIM}
  FROM docker.io/library/golang:1.26 AS build
  WORKDIR /src
  COPY go.mod go.sum ./
  RUN go mod download
  COPY . .
  RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/commonplace ./cmd/commonplace

  FROM scratch
  COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
  COPY --from=build /out/commonplace /commonplace
  EXPOSE 8101
  ENTRYPOINT ["/commonplace"]
  ```
  (The Containerfile won't `podman build` green until Task 2 lands the embedder that `cmd/commonplace` needs — note this in the commit; the file is correct, the binary completes in Task 2.)

- [ ] **1.10 — Commit.**
  ```
  cd /Users/jacinta/Source/commonplace && git add -A && git commit -m "commonplace-mvp: server scaffold + SQLite schema (entry/fts/vec) + Containerfile

Module github.com/CarriedWorldUniverse/commonplace. ncruces pure-Go
SQLite driver (FTS5 + brute-force-cosine vector blobs; vec1 named as
scale-up). entry_edge schema commented as the learning-paths growth
path, not built. Library + schema test green; cmd/ completes in Task 2.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
  ```

---

## Task 2 — Embedding seam (ollama default + fake) + vector helpers; build-green

Implement the `Embedder` interface from D2: a real ollama-backed embedder mirroring `nexus/runtime/providers/ollama-local/ollama.go`, a deterministic fake for tests, and the float32-BLOB encode/decode + cosine helpers that the brute-force vector path (D1) uses. This makes `cmd/commonplace` build (it references `NewOllamaEmbedder`) and gives Tasks 3–5 a testable embed seam.

- [ ] **2.1 — Ollama embedder test (TDD, httptest).** Create `/Users/jacinta/Source/commonplace/embed_ollama_test.go`:
  ```go
  package commonplace

  import (
  	"context"
  	"encoding/json"
  	"net/http"
  	"net/http/httptest"
  	"testing"
  )

  func TestOllamaEmbedderCallsEmbeddingsEndpoint(t *testing.T) {
  	var gotPath, gotModel, gotPrompt string
  	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  		gotPath = r.URL.Path
  		var body struct {
  			Model  string `json:"model"`
  			Prompt string `json:"prompt"`
  		}
  		_ = json.NewDecoder(r.Body).Decode(&body)
  		gotModel, gotPrompt = body.Model, body.Prompt
  		_ = json.NewEncoder(w).Encode(map[string]any{
  			"embedding": []float32{0.1, 0.2, 0.3, 0.4},
  		})
  	}))
  	defer srv.Close()

  	e, err := NewOllamaEmbedder(OllamaConfig{URL: srv.URL, Model: "test-model", Dim: 4})
  	if err != nil {
  		t.Fatalf("NewOllamaEmbedder: %v", err)
  	}
  	vec, err := e.Embed(context.Background(), "hello world")
  	if err != nil {
  		t.Fatalf("Embed: %v", err)
  	}
  	if gotPath != "/api/embeddings" {
  		t.Errorf("path = %q, want /api/embeddings", gotPath)
  	}
  	if gotModel != "test-model" || gotPrompt != "hello world" {
  		t.Errorf("model/prompt = %q/%q", gotModel, gotPrompt)
  	}
  	if len(vec) != 4 || vec[0] != 0.1 {
  		t.Errorf("vec = %v", vec)
  	}
  	if e.Dim() != 4 {
  		t.Errorf("Dim = %d, want 4", e.Dim())
  	}
  }

  func TestOllamaEmbedderEmptyTextErrors(t *testing.T) {
  	e, _ := NewOllamaEmbedder(OllamaConfig{URL: "http://unused", Model: "m", Dim: 4})
  	if _, err := e.Embed(context.Background(), ""); err == nil {
  		t.Fatal("expected error on empty text")
  	}
  }
  ```
  Run `go test . -run TestOllamaEmbedder`. Expect FAIL: `undefined: NewOllamaEmbedder`.

- [ ] **2.2 — Ollama embedder impl.** Replace `/Users/jacinta/Source/commonplace/embed.go` (keep the `Embedder` interface, add the impls + the growth-hook comment):
  ```go
  package commonplace

  import (
  	"bytes"
  	"context"
  	"encoding/json"
  	"fmt"
  	"io"
  	"net/http"
  	"time"
  )

  // Embedder turns text into a fixed-length vector. The store path embeds
  // content on write; search embeds the query. This is the AI-switchable
  // seam (plan D2) — ollama-backed in production, faked in tests. Swapping
  // to an API embedder later is a new Embedder impl, no caller change.
  type Embedder interface {
  	Embed(ctx context.Context, text string) ([]float32, error)
  	Dim() int
  }

  // GROWTH HOOK (not MVP): the vector layer is brute-force cosine over
  // []float32 blobs (plan D1). When the corpus outgrows a linear scan,
  // register the driver's pure-Go vec extension at service open —
  //   import "github.com/ncruces/go-sqlite3/ext/vec1"
  //   db, _ := driver.Open(dsn, vec1.Register)
  // and back the Searcher seam (search.go) with a `USING vec1` virtual
  // table instead of cosineRank. The seam is the only thing that changes.

  // OllamaConfig configures the default local embedder. Mirrors
  // nexus/runtime/providers/ollama-local.
  type OllamaConfig struct {
  	URL   string
  	Model string
  	Dim   int
  }

  // OllamaEmbedder calls a local ollama's /api/embeddings endpoint.
  type OllamaEmbedder struct {
  	url    string
  	model  string
  	dim    int
  	client *http.Client
  }

  // NewOllamaEmbedder validates config and returns the embedder.
  func NewOllamaEmbedder(cfg OllamaConfig) (*OllamaEmbedder, error) {
  	if cfg.URL == "" || cfg.Model == "" || cfg.Dim <= 0 {
  		return nil, fmt.Errorf("commonplace: OllamaConfig requires URL, Model, Dim>0")
  	}
  	return &OllamaEmbedder{
  		url:    cfg.URL,
  		model:  cfg.Model,
  		dim:    cfg.Dim,
  		client: &http.Client{Timeout: 30 * time.Second},
  	}, nil
  }

  func (e *OllamaEmbedder) Dim() int { return e.dim }

  type ollamaEmbedReq struct {
  	Model  string `json:"model"`
  	Prompt string `json:"prompt"`
  }
  type ollamaEmbedResp struct {
  	Embedding []float32 `json:"embedding"`
  }

  // Embed posts to {url}/api/embeddings and returns the vector.
  func (e *OllamaEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
  	if text == "" {
  		return nil, fmt.Errorf("commonplace: embed: empty text")
  	}
  	body, err := json.Marshal(ollamaEmbedReq{Model: e.model, Prompt: text})
  	if err != nil {
  		return nil, fmt.Errorf("commonplace: embed marshal: %w", err)
  	}
  	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.url+"/api/embeddings", bytes.NewReader(body))
  	if err != nil {
  		return nil, fmt.Errorf("commonplace: embed request: %w", err)
  	}
  	req.Header.Set("Content-Type", "application/json")
  	resp, err := e.client.Do(req)
  	if err != nil {
  		return nil, fmt.Errorf("commonplace: embed: ollama unreachable at %s: %w", e.url, err)
  	}
  	defer resp.Body.Close()
  	raw, err := io.ReadAll(resp.Body)
  	if err != nil {
  		return nil, fmt.Errorf("commonplace: embed read: %w", err)
  	}
  	if resp.StatusCode != http.StatusOK {
  		return nil, fmt.Errorf("commonplace: embed: ollama HTTP %d: %s", resp.StatusCode, truncate(string(raw), 200))
  	}
  	var parsed ollamaEmbedResp
  	if err := json.Unmarshal(raw, &parsed); err != nil {
  		return nil, fmt.Errorf("commonplace: embed parse: %w (body: %s)", err, truncate(string(raw), 200))
  	}
  	if len(parsed.Embedding) == 0 {
  		return nil, fmt.Errorf("commonplace: embed: empty embedding in response")
  	}
  	return parsed.Embedding, nil
  }

  func truncate(s string, max int) string {
  	if len(s) <= max {
  		return s
  	}
  	return s[:max] + "…"
  }
  ```
  Run `go test . -run TestOllamaEmbedder`. Expect PASS.

- [ ] **2.3 — Vector blob + cosine helpers test (TDD).** Create `/Users/jacinta/Source/commonplace/vector_test.go`:
  ```go
  package commonplace

  import (
  	"math"
  	"testing"
  )

  func TestEncodeDecodeRoundTrip(t *testing.T) {
  	v := []float32{1.5, -2.25, 0, 3.125}
  	blob := encodeVector(v)
  	got, err := decodeVector(blob)
  	if err != nil {
  		t.Fatalf("decodeVector: %v", err)
  	}
  	if len(got) != len(v) {
  		t.Fatalf("len = %d, want %d", len(got), len(v))
  	}
  	for i := range v {
  		if got[i] != v[i] {
  			t.Errorf("got[%d] = %v, want %v", i, got[i], v[i])
  		}
  	}
  }

  func TestCosineSimilarity(t *testing.T) {
  	a := []float32{1, 0, 0}
  	if s := cosine(a, a); math.Abs(float64(s)-1) > 1e-6 {
  		t.Errorf("cosine(a,a) = %v, want 1", s)
  	}
  	b := []float32{0, 1, 0}
  	if s := cosine(a, b); math.Abs(float64(s)) > 1e-6 {
  		t.Errorf("cosine(a,b) = %v, want 0", s)
  	}
  	c := []float32{2, 0, 0} // same direction as a, different magnitude
  	if s := cosine(a, c); math.Abs(float64(s)-1) > 1e-6 {
  		t.Errorf("cosine(a,c) = %v, want 1", s)
  	}
  }
  ```
  Run `go test . -run 'TestEncode|TestCosine'`. Expect FAIL: `undefined: encodeVector`.

- [ ] **2.4 — Vector helpers impl.** Create `/Users/jacinta/Source/commonplace/vector.go`:
  ```go
  package commonplace

  import (
  	"encoding/binary"
  	"fmt"
  	"math"
  )

  // encodeVector serializes a float32 vector as little-endian bytes for
  // the entry_vec.embedding BLOB column.
  func encodeVector(v []float32) []byte {
  	buf := make([]byte, 4*len(v))
  	for i, f := range v {
  		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
  	}
  	return buf
  }

  // decodeVector reverses encodeVector.
  func decodeVector(b []byte) ([]float32, error) {
  	if len(b)%4 != 0 {
  		return nil, fmt.Errorf("commonplace: vector blob length %d not a multiple of 4", len(b))
  	}
  	v := make([]float32, len(b)/4)
  	for i := range v {
  		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
  	}
  	return v, nil
  }

  // cosine returns the cosine similarity of two equal-length vectors.
  // Returns 0 for a zero-norm vector (no defined direction).
  func cosine(a, b []float32) float32 {
  	if len(a) != len(b) {
  		return 0
  	}
  	var dot, na, nb float64
  	for i := range a {
  		dot += float64(a[i]) * float64(b[i])
  		na += float64(a[i]) * float64(a[i])
  		nb += float64(b[i]) * float64(b[i])
  	}
  	if na == 0 || nb == 0 {
  		return 0
  	}
  	return float32(dot / (math.Sqrt(na) * math.Sqrt(nb)))
  }
  ```
  Run `go test . -run 'TestEncode|TestCosine'`. Expect PASS.

- [ ] **2.5 — Fake embedder for tests.** Create `/Users/jacinta/Source/commonplace/embed_fake_test.go` (a *_test.go file so it ships only in tests; deterministic, but with enough structure that conceptually-related text lands closer than unrelated text, so the Task 4 semantic-recall test is meaningful):
  ```go
  package commonplace

  import (
  	"context"
  	"hash/fnv"
  	"math"
  	"strings"
  )

  // fakeEmbedder is a deterministic bag-of-words embedder for tests. Each
  // token is hashed into a bucket and accumulated, then the vector is
  // L2-normalized. Texts sharing tokens (even reworded around shared
  // concept words) get higher cosine similarity than disjoint texts —
  // enough to exercise the hybrid-search semantic path without a live model.
  type fakeEmbedder struct{ dim int }

  func newFakeEmbedder(dim int) *fakeEmbedder { return &fakeEmbedder{dim: dim} }

  func (f *fakeEmbedder) Dim() int { return f.dim }

  func (f *fakeEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
  	v := make([]float32, f.dim)
  	for _, tok := range strings.Fields(strings.ToLower(text)) {
  		tok = strings.Trim(tok, ".,;:!?\"'()")
  		if tok == "" {
  			continue
  		}
  		h := fnv.New32a()
  		_, _ = h.Write([]byte(tok))
  		v[h.Sum32()%uint32(f.dim)] += 1
  	}
  	var norm float64
  	for _, x := range v {
  		norm += float64(x) * float64(x)
  	}
  	if norm > 0 {
  		n := float32(math.Sqrt(norm))
  		for i := range v {
  			v[i] /= n
  		}
  	}
  	return v, nil
  }
  ```
  NOTE: this is bag-of-words, so a *reworded* query only surfaces an entry if they share at least one concept token. The Task 4 semantic-recall test is written to share a concept word ("kubernetes") while differing in surrounding wording — proving the vector path contributes beyond exact-phrase FTS. A live `nomic-embed-text` deployment gives true paraphrase recall; the fake proves the wiring + fusion. State this limitation in the Task 4 commit.

- [ ] **2.6 — Build + test green.** Run:
  ```
  cd /Users/jacinta/Source/commonplace && go build ./... && go test ./...
  ```
  Expect: `go build ./...` now exits 0 (cmd/commonplace resolves `NewOllamaEmbedder`); `go test ./...` prints `ok  	github.com/CarriedWorldUniverse/commonplace`.

- [ ] **2.7 — Commit.**
  ```
  cd /Users/jacinta/Source/commonplace && git add -A && git commit -m "commonplace-mvp: embedding seam (ollama default + fake) + vector helpers

Embedder interface (AI-switchable, plan D2): OllamaEmbedder mirrors the
nexus ollama-local provider (/api/embeddings, nomic-embed-text/768).
Deterministic fakeEmbedder for tests. float32 blob encode/decode +
cosine for the brute-force vector path (plan D1). vec1 scale-up named
as a growth-hook comment. Binary + all tests green.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
  ```

---

## Task 3 — Store: `POST /api/knowledge`

Persist an entry, embed its content, and index it into FTS5 + entry_vec, owner from `X-CWB-Subject`, org from `X-CWB-Org`, visibility from the request body. Identity is read directly from headers here (a tiny inline reader); Task 6 formalizes it into middleware + context — Task 3's handler will be refactored to consume that context, but for now reads headers directly so the store path is testable standalone.

- [ ] **3.1 — Store unit test (TDD, service-level).** Create `/Users/jacinta/Source/commonplace/store_test.go`:
  ```go
  package commonplace

  import (
  	"context"
  	"testing"
  )

  func newTestService(t *testing.T) *Service {
  	t.Helper()
  	svc, err := New(context.Background(), Config{DBPath: ":memory:", Embedder: newFakeEmbedder(64)})
  	if err != nil {
  		t.Fatalf("New: %v", err)
  	}
  	t.Cleanup(func() { _ = svc.Close() })
  	return svc
  }

  func TestStorePersistsAndIndexes(t *testing.T) {
  	ctx := context.Background()
  	svc := newTestService(t)

  	e, err := svc.Store(ctx, StoreInput{
  		Org: "acme", Owner: "agent:builder",
  		Topic: "k8s rollout", Content: "how to roll out a kubernetes deployment safely",
  		Visibility: "org", Tags: []string{"ops", "k8s"},
  	})
  	if err != nil {
  		t.Fatalf("Store: %v", err)
  	}
  	if e.ID == "" {
  		t.Fatal("expected a minted id")
  	}
  	if e.Visibility != "org" {
  		t.Errorf("visibility = %q, want org", e.Visibility)
  	}

  	// entry row present.
  	var n int
  	_ = svc.db.QueryRowContext(ctx, `SELECT count(*) FROM entry WHERE id=?`, e.ID).Scan(&n)
  	if n != 1 {
  		t.Errorf("entry rows = %d, want 1", n)
  	}
  	// vector row present, correct dim.
  	var dim int
  	if err := svc.db.QueryRowContext(ctx, `SELECT dim FROM entry_vec WHERE entry_id=?`, e.ID).Scan(&dim); err != nil {
  		t.Fatalf("entry_vec missing: %v", err)
  	}
  	if dim != 64 {
  		t.Errorf("stored dim = %d, want 64", dim)
  	}
  	// FTS row present.
  	_ = svc.db.QueryRowContext(ctx, `SELECT count(*) FROM entry_fts WHERE entry_id=?`, e.ID).Scan(&n)
  	if n != 1 {
  		t.Errorf("fts rows = %d, want 1", n)
  	}
  }

  func TestStoreDefaultsVisibilityPrivate(t *testing.T) {
  	svc := newTestService(t)
  	e, err := svc.Store(context.Background(), StoreInput{
  		Org: "acme", Owner: "agent:x", Topic: "t", Content: "c",
  	})
  	if err != nil {
  		t.Fatalf("Store: %v", err)
  	}
  	if e.Visibility != "private" {
  		t.Errorf("default visibility = %q, want private", e.Visibility)
  	}
  }

  func TestStoreRejectsBadVisibility(t *testing.T) {
  	svc := newTestService(t)
  	_, err := svc.Store(context.Background(), StoreInput{
  		Org: "acme", Owner: "a", Topic: "t", Content: "c", Visibility: "public",
  	})
  	if err == nil {
  		t.Fatal("expected error on visibility=public")
  	}
  }
  ```
  Run `go test . -run TestStore`. Expect FAIL: `undefined: StoreInput` / `svc.Store`.

- [ ] **3.2 — Entry type + Store impl.** Create `/Users/jacinta/Source/commonplace/store.go`:
  ```go
  package commonplace

  import (
  	"context"
  	"crypto/rand"
  	"encoding/hex"
  	"encoding/json"
  	"fmt"
  	"time"
  )

  // Entry is a stored knowledge entry (spec §2/§3).
  type Entry struct {
  	ID         string    `json:"id"`
  	Org        string    `json:"org"`
  	Owner      string    `json:"owner"`
  	Topic      string    `json:"topic"`
  	Content    string    `json:"content"`
  	Visibility string    `json:"visibility"` // "private" | "org"
  	Tags       []string  `json:"tags"`
  	CreatedAt  time.Time `json:"created_at"`
  	UpdatedAt  time.Time `json:"updated_at"`
  }

  // StoreInput is the validated input to Store. Org+Owner come from the
  // gateway identity (X-CWB-Org / X-CWB-Subject); the rest from the body.
  type StoreInput struct {
  	Org        string
  	Owner      string
  	Topic      string
  	Content    string
  	Visibility string
  	Tags       []string
  }

  func validVisibility(v string) bool { return v == "private" || v == "org" }

  // newID mints an opaque 128-bit hex id.
  func newID() (string, error) {
  	var b [16]byte
  	if _, err := rand.Read(b[:]); err != nil {
  		return "", fmt.Errorf("commonplace: id: %w", err)
  	}
  	return hex.EncodeToString(b[:]), nil
  }

  // Store persists an entry, embeds its content, and indexes it into FTS5
  // + entry_vec in one transaction. Re-embed on update is in Task 5.
  func (s *Service) Store(ctx context.Context, in StoreInput) (Entry, error) {
  	if in.Org == "" || in.Owner == "" {
  		return Entry{}, fmt.Errorf("commonplace: store: org and owner required")
  	}
  	if in.Topic == "" || in.Content == "" {
  		return Entry{}, fmt.Errorf("commonplace: store: topic and content required")
  	}
  	if in.Visibility == "" {
  		in.Visibility = "private"
  	}
  	if !validVisibility(in.Visibility) {
  		return Entry{}, fmt.Errorf("commonplace: store: visibility must be private|org, got %q", in.Visibility)
  	}
  	if in.Tags == nil {
  		in.Tags = []string{}
  	}

  	vec, err := s.embedder.Embed(ctx, in.Topic+"\n"+in.Content)
  	if err != nil {
  		return Entry{}, fmt.Errorf("commonplace: store: embed: %w", err)
  	}

  	id, err := newID()
  	if err != nil {
  		return Entry{}, err
  	}
  	now := time.Now().UTC()
  	tagsJSON, err := json.Marshal(in.Tags)
  	if err != nil {
  		return Entry{}, fmt.Errorf("commonplace: store: marshal tags: %w", err)
  	}

  	tx, err := s.db.BeginTx(ctx, nil)
  	if err != nil {
  		return Entry{}, fmt.Errorf("commonplace: store: begin: %w", err)
  	}
  	defer func() { _ = tx.Rollback() }()

  	if _, err := tx.ExecContext(ctx,
  		`INSERT INTO entry(id, org, owner, topic, content, visibility, tags, created_at, updated_at)
  		 VALUES(?,?,?,?,?,?,?,?,?)`,
  		id, in.Org, in.Owner, in.Topic, in.Content, in.Visibility, string(tagsJSON),
  		now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
  		return Entry{}, fmt.Errorf("commonplace: store: insert entry: %w", err)
  	}
  	if _, err := tx.ExecContext(ctx,
  		`INSERT INTO entry_vec(entry_id, dim, embedding) VALUES(?,?,?)`,
  		id, len(vec), encodeVector(vec)); err != nil {
  		return Entry{}, fmt.Errorf("commonplace: store: insert vec: %w", err)
  	}
  	if _, err := tx.ExecContext(ctx,
  		`INSERT INTO entry_fts(topic, content, entry_id) VALUES(?,?,?)`,
  		in.Topic, in.Content, id); err != nil {
  		return Entry{}, fmt.Errorf("commonplace: store: insert fts: %w", err)
  	}
  	if err := tx.Commit(); err != nil {
  		return Entry{}, fmt.Errorf("commonplace: store: commit: %w", err)
  	}

  	return Entry{
  		ID: id, Org: in.Org, Owner: in.Owner, Topic: in.Topic, Content: in.Content,
  		Visibility: in.Visibility, Tags: in.Tags, CreatedAt: now, UpdatedAt: now,
  	}, nil
  }
  ```
  Run `go test . -run TestStore`. Expect PASS.

- [ ] **3.3 — HTTP handler test (TDD).** Create `/Users/jacinta/Source/commonplace/handlers_store_test.go`:
  ```go
  package commonplace

  import (
  	"net/http"
  	"net/http/httptest"
  	"strings"
  	"testing"
  )

  func TestPostKnowledgeStores(t *testing.T) {
  	svc := newTestService(t)
  	body := `{"topic":"k8s rollout","content":"roll out a kubernetes deployment","visibility":"org","tags":["ops"]}`
  	req := httptest.NewRequest(http.MethodPost, "/api/knowledge", strings.NewReader(body))
  	req.Header.Set("X-CWB-Org", "acme")
  	req.Header.Set("X-CWB-Subject", "agent:builder")
  	req.Header.Set("X-CWB-Scopes", "knowledge:read knowledge:write")
  	rec := httptest.NewRecorder()

  	svc.Handler().ServeHTTP(rec, req)

  	if rec.Code != http.StatusCreated {
  		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
  	}
  	if !strings.Contains(rec.Body.String(), `"owner":"agent:builder"`) {
  		t.Errorf("response missing owner: %s", rec.Body.String())
  	}
  }

  func TestPostKnowledgeRequiresWriteScope(t *testing.T) {
  	svc := newTestService(t)
  	req := httptest.NewRequest(http.MethodPost, "/api/knowledge", strings.NewReader(`{"topic":"t","content":"c"}`))
  	req.Header.Set("X-CWB-Org", "acme")
  	req.Header.Set("X-CWB-Subject", "agent:builder")
  	req.Header.Set("X-CWB-Scopes", "knowledge:read") // no write
  	rec := httptest.NewRecorder()
  	svc.Handler().ServeHTTP(rec, req)
  	if rec.Code != http.StatusForbidden {
  		t.Fatalf("status = %d, want 403", rec.Code)
  	}
  }
  ```
  Run `go test . -run TestPostKnowledge`. Expect FAIL (no POST route; healthz-only mux).

- [ ] **3.4 — Identity reader + store handler.** Create `/Users/jacinta/Source/commonplace/identity.go` (the inline reader; Task 6 promotes it to middleware-injected context but keeps these helpers):
  ```go
  package commonplace

  import (
  	"net/http"
  	"strings"
  )

  // Identity is the gateway-verified caller (plan D6). Read from the
  // mTLS-injected X-CWB-* headers; never re-verified here.
  type Identity struct {
  	Subject string
  	Org     string
  	Kind    string
  	Scopes  []string
  }

  // identityFromRequest reads the trusted X-CWB-* headers. The gateway
  // strips any client-supplied copies before injecting verified values,
  // so these are trustworthy on the ClusterIP hop.
  func identityFromRequest(r *http.Request) Identity {
  	return Identity{
  		Subject: r.Header.Get("X-CWB-Subject"),
  		Org:     r.Header.Get("X-CWB-Org"),
  		Kind:    r.Header.Get("X-CWB-Kind"),
  		Scopes:  strings.Fields(r.Header.Get("X-CWB-Scopes")),
  	}
  }

  func (id Identity) hasScope(want string) bool {
  	for _, s := range id.Scopes {
  		if s == want {
  			return true
  		}
  	}
  	return false
  }

  const (
  	scopeRead  = "knowledge:read"
  	scopeWrite = "knowledge:write"
  )
  ```
  Add the store handler to `handlers.go` (rewrite the file to register the route + a JSON helper):
  ```go
  package commonplace

  import (
  	"encoding/json"
  	"net/http"
  )

  // Handler returns the service's HTTP handler.
  func (s *Service) Handler() http.Handler {
  	mux := http.NewServeMux()
  	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
  		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "commonplace"})
  	})
  	mux.HandleFunc("POST /api/knowledge", s.handleStore)
  	return mux
  }

  func writeJSON(w http.ResponseWriter, code int, v any) {
  	w.Header().Set("Content-Type", "application/json")
  	w.WriteHeader(code)
  	_ = json.NewEncoder(w).Encode(v)
  }

  func writeErr(w http.ResponseWriter, code int, msg string) {
  	writeJSON(w, code, map[string]string{"error": msg})
  }

  type storeBody struct {
  	Topic      string   `json:"topic"`
  	Content    string   `json:"content"`
  	Visibility string   `json:"visibility"`
  	Tags       []string `json:"tags"`
  }

  func (s *Service) handleStore(w http.ResponseWriter, r *http.Request) {
  	id := identityFromRequest(r)
  	if id.Subject == "" || id.Org == "" {
  		writeErr(w, http.StatusUnauthorized, "missing identity")
  		return
  	}
  	if !id.hasScope(scopeWrite) {
  		writeErr(w, http.StatusForbidden, "knowledge:write required")
  		return
  	}
  	var body storeBody
  	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
  		writeErr(w, http.StatusBadRequest, "invalid json")
  		return
  	}
  	entry, err := s.Store(r.Context(), StoreInput{
  		Org: id.Org, Owner: id.Subject,
  		Topic: body.Topic, Content: body.Content,
  		Visibility: body.Visibility, Tags: body.Tags,
  	})
  	if err != nil {
  		writeErr(w, http.StatusBadRequest, err.Error())
  		return
  	}
  	writeJSON(w, http.StatusCreated, entry)
  }
  ```
  Run `go test . -run TestPostKnowledge`. Expect PASS.

- [ ] **3.5 — Full suite + commit.**
  ```
  cd /Users/jacinta/Source/commonplace && go build ./... && go test ./... && git add -A && git commit -m "commonplace-mvp: store path — POST /api/knowledge

Service.Store persists entry + embeds content + indexes into FTS5 and
entry_vec in one tx; owner from X-CWB-Subject, org from X-CWB-Org,
visibility (private|org) from the body, defaulting private. Write path
gated on knowledge:write. Identity read inline from trusted X-CWB-*
headers (promoted to middleware in Task 6).

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
  ```

---

## Task 4 — Hybrid search: `GET /api/knowledge/search?q=...` (the core)

The DoD heart. Embed the query → brute-force cosine rank over the org's visible entry_vec rows (vector NN) ⊕ FTS5 keyword rank, fused with RRF (D3), scoped to org + visibility (own private + org-shared), returned ranked with fused scores. A test proves a **differently-worded conceptual query surfaces a stored entry** beyond exact-term match.

- [ ] **4.1 — Searcher seam + search test (TDD, semantic recall).** Create `/Users/jacinta/Source/commonplace/search_test.go`:
  ```go
  package commonplace

  import (
  	"context"
  	"testing"
  )

  func TestHybridSearchSemanticRecall(t *testing.T) {
  	ctx := context.Background()
  	svc := newTestService(t)

  	// Store an entry phrased one way.
  	target, err := svc.Store(ctx, StoreInput{
  		Org: "acme", Owner: "agent:a", Visibility: "org",
  		Topic:   "kubernetes deployment rollout",
  		Content: "safely roll out a new container image to a kubernetes cluster with health checks",
  	})
  	if err != nil {
  		t.Fatalf("store target: %v", err)
  	}
  	// Store a distractor on an unrelated concept.
  	if _, err := svc.Store(ctx, StoreInput{
  		Org: "acme", Owner: "agent:a", Visibility: "org",
  		Topic: "invoice formatting", Content: "how to format a PDF invoice for accounting",
  	}); err != nil {
  		t.Fatalf("store distractor: %v", err)
  	}

  	// Query with DIFFERENT wording, sharing the concept token "kubernetes"
  	// but not the exact phrase — semantic+keyword fusion should surface
  	// the target ranked first.
  	res, err := svc.Search(ctx, SearchInput{
  		Org: "acme", Caller: "agent:a",
  		Query: "updating a kubernetes service without downtime",
  		TopK:  5,
  	})
  	if err != nil {
  		t.Fatalf("Search: %v", err)
  	}
  	if len(res) == 0 {
  		t.Fatal("expected at least one hit")
  	}
  	if res[0].Entry.ID != target.ID {
  		t.Fatalf("top hit = %q (%q), want target %q", res[0].Entry.ID, res[0].Entry.Topic, target.ID)
  	}
  	if res[0].Score <= 0 {
  		t.Errorf("fused score = %v, want > 0", res[0].Score)
  	}
  }

  func TestSearchScopesToOrgAndVisibility(t *testing.T) {
  	ctx := context.Background()
  	svc := newTestService(t)

  	// Another org's entry must never surface.
  	if _, err := svc.Store(ctx, StoreInput{Org: "other", Owner: "x", Visibility: "org",
  		Topic: "kubernetes", Content: "kubernetes in another org"}); err != nil {
  		t.Fatal(err)
  	}
  	// Another owner's PRIVATE entry in my org must not surface.
  	if _, err := svc.Store(ctx, StoreInput{Org: "acme", Owner: "agent:b", Visibility: "private",
  		Topic: "kubernetes", Content: "private kubernetes notes by b"}); err != nil {
  		t.Fatal(err)
  	}
  	// My own private entry SHOULD surface.
  	mine, err := svc.Store(ctx, StoreInput{Org: "acme", Owner: "agent:a", Visibility: "private",
  		Topic: "kubernetes", Content: "my private kubernetes notes"})
  	if err != nil {
  		t.Fatal(err)
  	}

  	res, err := svc.Search(ctx, SearchInput{Org: "acme", Caller: "agent:a", Query: "kubernetes", TopK: 10})
  	if err != nil {
  		t.Fatalf("Search: %v", err)
  	}
  	for _, h := range res {
  		if h.Entry.Org != "acme" {
  			t.Errorf("cross-org leak: %q", h.Entry.Org)
  		}
  		if h.Entry.Visibility == "private" && h.Entry.Owner != "agent:a" {
  			t.Errorf("foreign private leak: owner %q", h.Entry.Owner)
  		}
  	}
  	var sawMine bool
  	for _, h := range res {
  		if h.Entry.ID == mine.ID {
  			sawMine = true
  		}
  	}
  	if !sawMine {
  		t.Error("expected my own private entry to surface")
  	}
  }
  ```
  Run `go test . -run 'TestHybridSearch|TestSearchScopes'`. Expect FAIL: `undefined: SearchInput` / `svc.Search`.

- [ ] **4.2 — Search impl (RRF fusion over scoped candidates).** Create `/Users/jacinta/Source/commonplace/search.go`:
  ```go
  package commonplace

  import (
  	"context"
  	"database/sql"
  	"fmt"
  	"sort"
  )

  // rrfK is the reciprocal-rank-fusion constant (plan D3). Canonical default.
  const rrfK = 60.0

  // SearchInput parameterizes a hybrid search. Org + Caller come from the
  // gateway identity; the visibility scope is (own private) ∪ (org-shared).
  type SearchInput struct {
  	Org    string
  	Caller string // X-CWB-Subject
  	Query  string
  	TopK   int
  }

  // Hit is one ranked search result.
  type Hit struct {
  	Entry Entry   `json:"entry"`
  	Score float64 `json:"score"` // fused RRF score; higher = better
  }

  // candidate is an entry visible to the caller, with its embedding.
  type candidate struct {
  	entry Entry
  	vec   []float32
  }

  // visibleEntries loads every entry in the caller's org that the caller may
  // see: own (any visibility) ∪ org-shared (visibility='org'). This is the
  // scoping seam — brute-force cosine ranks within this set (plan D1).
  func (s *Service) visibleEntries(ctx context.Context, org, caller string) ([]candidate, error) {
  	rows, err := s.db.QueryContext(ctx, `
  		SELECT e.id, e.org, e.owner, e.topic, e.content, e.visibility, e.tags,
  		       e.created_at, e.updated_at, v.embedding
  		FROM entry e JOIN entry_vec v ON v.entry_id = e.id
  		WHERE e.org = ? AND (e.owner = ? OR e.visibility = 'org')`,
  		org, caller)
  	if err != nil {
  		return nil, fmt.Errorf("commonplace: search: query candidates: %w", err)
  	}
  	defer rows.Close()
  	var out []candidate
  	for rows.Next() {
  		var (
  			e        Entry
  			tags     string
  			blob     []byte
  			created  string
  			updated  string
  		)
  		if err := rows.Scan(&e.ID, &e.Org, &e.Owner, &e.Topic, &e.Content,
  			&e.Visibility, &tags, &created, &updated, &blob); err != nil {
  			return nil, fmt.Errorf("commonplace: search: scan: %w", err)
  		}
  		e.Tags = parseTags(tags)
  		e.CreatedAt = parseTime(created)
  		e.UpdatedAt = parseTime(updated)
  		vec, err := decodeVector(blob)
  		if err != nil {
  			return nil, err
  		}
  		out = append(out, candidate{entry: e, vec: vec})
  	}
  	return out, rows.Err()
  }

  // ftsRank returns entry ids ranked best-first by FTS5 bm25 over the
  // caller's visible set. Scoped by joining the same visibility predicate.
  func (s *Service) ftsRank(ctx context.Context, org, caller, query string) ([]string, error) {
  	if query == "" {
  		return nil, nil
  	}
  	rows, err := s.db.QueryContext(ctx, `
  		SELECT f.entry_id
  		FROM entry_fts f
  		JOIN entry e ON e.id = f.entry_id
  		WHERE entry_fts MATCH ?
  		  AND e.org = ? AND (e.owner = ? OR e.visibility = 'org')
  		ORDER BY bm25(entry_fts)`,
  		ftsQuery(query), org, caller)
  	if err != nil {
  		// A malformed FTS MATCH (rare; odd punctuation) is non-fatal —
  		// degrade to vector-only rather than failing the whole search.
  		return nil, nil
  	}
  	defer rows.Close()
  	var ids []string
  	for rows.Next() {
  		var id string
  		if err := rows.Scan(&id); err != nil {
  			return nil, err
  		}
  		ids = append(ids, id)
  	}
  	return ids, rows.Err()
  }

  // Search runs hybrid retrieval: embed the query → cosine-rank the visible
  // vectors, ⊕ FTS5 keyword rank, fused by RRF, top-K (plan D3/D1).
  func (s *Service) Search(ctx context.Context, in SearchInput) ([]Hit, error) {
  	if in.Org == "" || in.Caller == "" {
  		return nil, fmt.Errorf("commonplace: search: org and caller required")
  	}
  	if in.TopK <= 0 {
  		in.TopK = 10
  	}

  	cands, err := s.visibleEntries(ctx, in.Org, in.Caller)
  	if err != nil {
  		return nil, err
  	}
  	if len(cands) == 0 {
  		return nil, nil
  	}

  	// Vector ranking: cosine of query vs each candidate, best-first.
  	qvec, err := s.embedder.Embed(ctx, in.Query)
  	if err != nil {
  		return nil, fmt.Errorf("commonplace: search: embed query: %w", err)
  	}
  	type scored struct {
  		id  string
  		sim float32
  	}
  	vscored := make([]scored, len(cands))
  	for i, c := range cands {
  		vscored[i] = scored{id: c.entry.ID, sim: cosine(qvec, c.vec)}
  	}
  	sort.Slice(vscored, func(i, j int) bool { return vscored[i].sim > vscored[j].sim })
  	vecRank := make([]string, len(vscored))
  	for i, s := range vscored {
  		vecRank[i] = s.id
  	}

  	// Keyword ranking via FTS5.
  	ftsRanked, err := s.ftsRank(ctx, in.Org, in.Caller, in.Query)
  	if err != nil {
  		return nil, err
  	}

  	// Reciprocal Rank Fusion.
  	fused := map[string]float64{}
  	for rank, id := range vecRank {
  		fused[id] += 1.0 / (rrfK + float64(rank+1))
  	}
  	for rank, id := range ftsRanked {
  		fused[id] += 1.0 / (rrfK + float64(rank+1))
  	}

  	byID := make(map[string]Entry, len(cands))
  	for _, c := range cands {
  		byID[c.entry.ID] = c.entry
  	}
  	hits := make([]Hit, 0, len(fused))
  	for id, score := range fused {
  		hits = append(hits, Hit{Entry: byID[id], Score: score})
  	}
  	// Deterministic order: score desc, then id asc as a tiebreak.
  	sort.Slice(hits, func(i, j int) bool {
  		if hits[i].Score != hits[j].Score {
  			return hits[i].Score > hits[j].Score
  		}
  		return hits[i].Entry.ID < hits[j].Entry.ID
  	})
  	if len(hits) > in.TopK {
  		hits = hits[:in.TopK]
  	}
  	return hits, nil
  }

  // (helpers parseTags/parseTime/ftsQuery live in store.go's sibling — see 4.3)
  var _ = sql.ErrNoRows
  ```

- [ ] **4.3 — Shared helpers (tags/time parse + FTS query sanitize).** Create `/Users/jacinta/Source/commonplace/util.go`:
  ```go
  package commonplace

  import (
  	"encoding/json"
  	"strings"
  	"time"
  )

  func parseTags(s string) []string {
  	var out []string
  	if err := json.Unmarshal([]byte(s), &out); err != nil || out == nil {
  		return []string{}
  	}
  	return out
  }

  func parseTime(s string) time.Time {
  	t, err := time.Parse(time.RFC3339Nano, s)
  	if err != nil {
  		return time.Time{}
  	}
  	return t
  }

  // ftsQuery turns a free-text query into a safe FTS5 MATCH expression: each
  // alphanumeric token becomes a prefix-OR term. Avoids FTS5 syntax errors
  // from punctuation in user queries and gives reasonable keyword recall.
  func ftsQuery(q string) string {
  	var terms []string
  	for _, f := range strings.Fields(q) {
  		var b strings.Builder
  		for _, r := range f {
  			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
  				b.WriteRune(r)
  			}
  		}
  		if b.Len() > 0 {
  			terms = append(terms, `"`+b.String()+`"`)
  		}
  	}
  	if len(terms) == 0 {
  		return `""`
  	}
  	return strings.Join(terms, " OR ")
  }
  ```
  Remove the now-unneeded `var _ = sql.ErrNoRows` line and the `database/sql` import from `search.go` if unused (run `go vet`; fix imports). Run `go test . -run 'TestHybridSearch|TestSearchScopes'`. Expect PASS (the fake embedder shares the "kubernetes" token → cosine ranks the target above the invoice distractor; FTS reinforces). If the semantic-recall test is flaky on the fake embedder, confirm the query and target share at least one concept token — that is the fake's recall mechanism (real ollama gives true paraphrase recall; documented in 2.5).

- [ ] **4.4 — Search HTTP handler test (TDD).** Create `/Users/jacinta/Source/commonplace/handlers_search_test.go`:
  ```go
  package commonplace

  import (
  	"encoding/json"
  	"net/http"
  	"net/http/httptest"
  	"net/url"
  	"strings"
  	"testing"
  )

  func storeViaHTTP(t *testing.T, svc *Service, org, subj, topic, content, vis string) {
  	t.Helper()
  	b := `{"topic":"` + topic + `","content":"` + content + `","visibility":"` + vis + `"}`
  	req := httptest.NewRequest(http.MethodPost, "/api/knowledge", strings.NewReader(b))
  	req.Header.Set("X-CWB-Org", org)
  	req.Header.Set("X-CWB-Subject", subj)
  	req.Header.Set("X-CWB-Scopes", "knowledge:read knowledge:write")
  	rec := httptest.NewRecorder()
  	svc.Handler().ServeHTTP(rec, req)
  	if rec.Code != http.StatusCreated {
  		t.Fatalf("store status %d: %s", rec.Code, rec.Body.String())
  	}
  }

  func TestGetSearchReturnsRankedHits(t *testing.T) {
  	svc := newTestService(t)
  	storeViaHTTP(t, svc, "acme", "agent:a", "kubernetes rollout", "roll out a kubernetes deployment", "org")
  	storeViaHTTP(t, svc, "acme", "agent:a", "invoice", "format a pdf invoice", "org")

  	u := "/api/knowledge/search?q=" + url.QueryEscape("update a kubernetes service") + "&top_k=5"
  	req := httptest.NewRequest(http.MethodGet, u, nil)
  	req.Header.Set("X-CWB-Org", "acme")
  	req.Header.Set("X-CWB-Subject", "agent:a")
  	req.Header.Set("X-CWB-Scopes", "knowledge:read")
  	rec := httptest.NewRecorder()
  	svc.Handler().ServeHTTP(rec, req)

  	if rec.Code != http.StatusOK {
  		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
  	}
  	var out struct {
  		Hits []Hit `json:"hits"`
  	}
  	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
  		t.Fatalf("decode: %v", err)
  	}
  	if len(out.Hits) == 0 || !strings.Contains(out.Hits[0].Entry.Topic, "kubernetes") {
  		t.Fatalf("expected kubernetes entry top; got %+v", out.Hits)
  	}
  }

  func TestGetSearchRequiresReadScope(t *testing.T) {
  	svc := newTestService(t)
  	req := httptest.NewRequest(http.MethodGet, "/api/knowledge/search?q=x", nil)
  	req.Header.Set("X-CWB-Org", "acme")
  	req.Header.Set("X-CWB-Subject", "agent:a")
  	// no scopes
  	rec := httptest.NewRecorder()
  	svc.Handler().ServeHTTP(rec, req)
  	if rec.Code != http.StatusForbidden {
  		t.Fatalf("status %d, want 403", rec.Code)
  	}
  }
  ```
  Run `go test . -run TestGetSearch`. Expect FAIL (no search route).

- [ ] **4.5 — Search handler.** Add to `handlers.go`: register `mux.HandleFunc("GET /api/knowledge/search", s.handleSearch)` (place it BEFORE any `GET /api/knowledge/{id}` route from Task 5 so the literal path wins — Go 1.22+ ServeMux prefers more-specific patterns, but keep search as a distinct literal segment to avoid ambiguity), and add:
  ```go
  func (s *Service) handleSearch(w http.ResponseWriter, r *http.Request) {
  	id := identityFromRequest(r)
  	if id.Subject == "" || id.Org == "" {
  		writeErr(w, http.StatusUnauthorized, "missing identity")
  		return
  	}
  	if !id.hasScope(scopeRead) {
  		writeErr(w, http.StatusForbidden, "knowledge:read required")
  		return
  	}
  	q := r.URL.Query().Get("q")
  	if q == "" {
  		writeErr(w, http.StatusBadRequest, "q required")
  		return
  	}
  	topK := 10
  	if v := r.URL.Query().Get("top_k"); v != "" {
  		if n, err := strconv.Atoi(v); err == nil && n > 0 {
  			topK = n
  		}
  	}
  	hits, err := s.Search(r.Context(), SearchInput{Org: id.Org, Caller: id.Subject, Query: q, TopK: topK})
  	if err != nil {
  		writeErr(w, http.StatusInternalServerError, err.Error())
  		return
  	}
  	if hits == nil {
  		hits = []Hit{}
  	}
  	writeJSON(w, http.StatusOK, map[string]any{"hits": hits})
  }
  ```
  Add `"strconv"` to `handlers.go` imports. Run `go test . -run TestGetSearch`. Expect PASS.

- [ ] **4.6 — Full suite + commit.**
  ```
  cd /Users/jacinta/Source/commonplace && go build ./... && go test ./... && git add -A && git commit -m "commonplace-mvp: hybrid semantic+keyword search — GET /api/knowledge/search

The core. Embed query -> brute-force cosine over the caller's visible
entry_vec rows (vector NN) fused with FTS5 bm25 keyword via RRF (k=60,
plan D3). Scoped to org + visibility (own private union org-shared).
Semantic-recall test: a differently-worded conceptual query surfaces
the stored entry above an unrelated distractor (fake embedder shares a
concept token; live nomic-embed-text gives true paraphrase recall).
Cross-org + foreign-private isolation asserted.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
  ```

---

## Task 5 — Retrieve / list / update (re-embed) / delete

`GET /api/knowledge/{id}`, `GET /api/knowledge`, `PATCH /api/knowledge/{id}` (re-embeds on content/topic change, D5), `DELETE /api/knowledge/{id}`. All org+visibility scoped; mutations owner-restricted to the entry's owner.

- [ ] **5.1 — CRUD service test (TDD).** Create `/Users/jacinta/Source/commonplace/crud_test.go`:
  ```go
  package commonplace

  import (
  	"context"
  	"testing"
  )

  func TestGetByIDRespectsScope(t *testing.T) {
  	ctx := context.Background()
  	svc := newTestService(t)
  	priv, _ := svc.Store(ctx, StoreInput{Org: "acme", Owner: "agent:b", Visibility: "private", Topic: "t", Content: "c"})
  	shared, _ := svc.Store(ctx, StoreInput{Org: "acme", Owner: "agent:b", Visibility: "org", Topic: "t2", Content: "c2"})

  	// agent:a may read the org-shared one.
  	if _, err := svc.Get(ctx, "acme", "agent:a", shared.ID); err != nil {
  		t.Errorf("Get org-shared: %v", err)
  	}
  	// agent:a may NOT read agent:b's private one.
  	if _, err := svc.Get(ctx, "acme", "agent:a", priv.ID); err == nil {
  		t.Error("expected not-found on foreign private")
  	}
  	// cross-org: never.
  	if _, err := svc.Get(ctx, "other", "agent:a", shared.ID); err == nil {
  		t.Error("expected not-found cross-org")
  	}
  }

  func TestListReturnsVisible(t *testing.T) {
  	ctx := context.Background()
  	svc := newTestService(t)
  	_, _ = svc.Store(ctx, StoreInput{Org: "acme", Owner: "agent:a", Visibility: "private", Topic: "mine", Content: "x"})
  	_, _ = svc.Store(ctx, StoreInput{Org: "acme", Owner: "agent:b", Visibility: "org", Topic: "shared", Content: "y"})
  	_, _ = svc.Store(ctx, StoreInput{Org: "acme", Owner: "agent:b", Visibility: "private", Topic: "hidden", Content: "z"})
  	list, err := svc.List(ctx, "acme", "agent:a")
  	if err != nil {
  		t.Fatalf("List: %v", err)
  	}
  	if len(list) != 2 {
  		t.Fatalf("list len = %d, want 2 (mine + shared)", len(list))
  	}
  }

  func TestUpdateReembeds(t *testing.T) {
  	ctx := context.Background()
  	svc := newTestService(t)
  	e, _ := svc.Store(ctx, StoreInput{Org: "acme", Owner: "agent:a", Topic: "t", Content: "original content"})

  	var before []byte
  	_ = svc.db.QueryRowContext(ctx, `SELECT embedding FROM entry_vec WHERE entry_id=?`, e.ID).Scan(&before)

  	newContent := "completely different replacement text"
  	upd, err := svc.Update(ctx, "acme", "agent:a", e.ID, UpdateInput{Content: &newContent})
  	if err != nil {
  		t.Fatalf("Update: %v", err)
  	}
  	if upd.Content != newContent {
  		t.Errorf("content = %q", upd.Content)
  	}
  	var after []byte
  	_ = svc.db.QueryRowContext(ctx, `SELECT embedding FROM entry_vec WHERE entry_id=?`, e.ID).Scan(&after)
  	if string(before) == string(after) {
  		t.Error("expected embedding to change after content update (re-embed)")
  	}
  }

  func TestUpdateRejectsNonOwner(t *testing.T) {
  	ctx := context.Background()
  	svc := newTestService(t)
  	e, _ := svc.Store(ctx, StoreInput{Org: "acme", Owner: "agent:a", Visibility: "org", Topic: "t", Content: "c"})
  	nc := "x"
  	if _, err := svc.Update(ctx, "acme", "agent:b", e.ID, UpdateInput{Content: &nc}); err == nil {
  		t.Error("expected non-owner update to fail")
  	}
  }

  func TestDeleteRemovesAllIndexes(t *testing.T) {
  	ctx := context.Background()
  	svc := newTestService(t)
  	e, _ := svc.Store(ctx, StoreInput{Org: "acme", Owner: "agent:a", Topic: "t", Content: "c"})
  	if err := svc.Delete(ctx, "acme", "agent:a", e.ID); err != nil {
  		t.Fatalf("Delete: %v", err)
  	}
  	for _, q := range []string{
  		`SELECT count(*) FROM entry WHERE id=?`,
  		`SELECT count(*) FROM entry_vec WHERE entry_id=?`,
  		`SELECT count(*) FROM entry_fts WHERE entry_id=?`,
  	} {
  		var n int
  		_ = svc.db.QueryRowContext(ctx, q, e.ID).Scan(&n)
  		if n != 0 {
  			t.Errorf("%q left %d rows", q, n)
  		}
  	}
  }
  ```
  Run `go test . -run 'TestGetByID|TestList|TestUpdate|TestDelete'`. Expect FAIL: undefined methods.

- [ ] **5.2 — CRUD impl.** Create `/Users/jacinta/Source/commonplace/crud.go`:
  ```go
  package commonplace

  import (
  	"context"
  	"database/sql"
  	"errors"
  	"fmt"
  	"time"
  )

  // ErrNotFound is returned when an entry doesn't exist or isn't visible to
  // the caller (the two are deliberately indistinguishable — no existence
  // oracle across visibility/org boundaries).
  var ErrNotFound = errors.New("commonplace: not found")

  // ErrForbidden is returned when a mutation targets an entry the caller
  // does not own.
  var ErrForbidden = errors.New("commonplace: forbidden")

  // Get returns one entry if visible to the caller (own, or org-shared in
  // the caller's org).
  func (s *Service) Get(ctx context.Context, org, caller, id string) (Entry, error) {
  	row := s.db.QueryRowContext(ctx, `
  		SELECT id, org, owner, topic, content, visibility, tags, created_at, updated_at
  		FROM entry
  		WHERE id = ? AND org = ? AND (owner = ? OR visibility = 'org')`,
  		id, org, caller)
  	return scanEntry(row)
  }

  // List returns all entries visible to the caller, newest first.
  func (s *Service) List(ctx context.Context, org, caller string) ([]Entry, error) {
  	rows, err := s.db.QueryContext(ctx, `
  		SELECT id, org, owner, topic, content, visibility, tags, created_at, updated_at
  		FROM entry
  		WHERE org = ? AND (owner = ? OR visibility = 'org')
  		ORDER BY updated_at DESC`,
  		org, caller)
  	if err != nil {
  		return nil, fmt.Errorf("commonplace: list: %w", err)
  	}
  	defer rows.Close()
  	var out []Entry
  	for rows.Next() {
  		e, err := scanEntryRows(rows)
  		if err != nil {
  			return nil, err
  		}
  		out = append(out, e)
  	}
  	return out, rows.Err()
  }

  // UpdateInput carries optional field changes. Nil pointers are unchanged.
  type UpdateInput struct {
  	Topic      *string
  	Content    *string
  	Visibility *string
  	Tags       *[]string
  }

  // Update mutates an owned entry. If topic or content changes, the entry is
  // re-embedded and re-FTS-indexed synchronously (plan D5).
  func (s *Service) Update(ctx context.Context, org, caller, id string, in UpdateInput) (Entry, error) {
  	cur, err := s.ownedEntry(ctx, org, caller, id)
  	if err != nil {
  		return Entry{}, err
  	}
  	if in.Topic != nil {
  		cur.Topic = *in.Topic
  	}
  	if in.Content != nil {
  		cur.Content = *in.Content
  	}
  	if in.Visibility != nil {
  		if !validVisibility(*in.Visibility) {
  			return Entry{}, fmt.Errorf("commonplace: update: visibility must be private|org")
  		}
  		cur.Visibility = *in.Visibility
  	}
  	if in.Tags != nil {
  		cur.Tags = *in.Tags
  	}
  	reindex := in.Topic != nil || in.Content != nil
  	now := time.Now().UTC()
  	cur.UpdatedAt = now

  	tx, err := s.db.BeginTx(ctx, nil)
  	if err != nil {
  		return Entry{}, fmt.Errorf("commonplace: update: begin: %w", err)
  	}
  	defer func() { _ = tx.Rollback() }()

  	tagsJSON, _ := jsonTags(cur.Tags)
  	if _, err := tx.ExecContext(ctx,
  		`UPDATE entry SET topic=?, content=?, visibility=?, tags=?, updated_at=? WHERE id=?`,
  		cur.Topic, cur.Content, cur.Visibility, tagsJSON, now.Format(time.RFC3339Nano), id); err != nil {
  		return Entry{}, fmt.Errorf("commonplace: update: entry: %w", err)
  	}
  	if reindex {
  		vec, err := s.embedder.Embed(ctx, cur.Topic+"\n"+cur.Content)
  		if err != nil {
  			return Entry{}, fmt.Errorf("commonplace: update: re-embed: %w", err)
  		}
  		if _, err := tx.ExecContext(ctx,
  			`UPDATE entry_vec SET dim=?, embedding=? WHERE entry_id=?`,
  			len(vec), encodeVector(vec), id); err != nil {
  			return Entry{}, fmt.Errorf("commonplace: update: vec: %w", err)
  		}
  		// FTS5 has no UPDATE-in-place for external content rows we own;
  		// delete + re-insert keeps the index exact.
  		if _, err := tx.ExecContext(ctx, `DELETE FROM entry_fts WHERE entry_id=?`, id); err != nil {
  			return Entry{}, fmt.Errorf("commonplace: update: fts del: %w", err)
  		}
  		if _, err := tx.ExecContext(ctx,
  			`INSERT INTO entry_fts(topic, content, entry_id) VALUES(?,?,?)`,
  			cur.Topic, cur.Content, id); err != nil {
  			return Entry{}, fmt.Errorf("commonplace: update: fts ins: %w", err)
  		}
  	}
  	if err := tx.Commit(); err != nil {
  		return Entry{}, fmt.Errorf("commonplace: update: commit: %w", err)
  	}
  	return cur, nil
  }

  // Delete removes an owned entry and its indexes. The entry_vec FK is
  // ON DELETE CASCADE; FTS is removed explicitly.
  func (s *Service) Delete(ctx context.Context, org, caller, id string) error {
  	if _, err := s.ownedEntry(ctx, org, caller, id); err != nil {
  		return err
  	}
  	tx, err := s.db.BeginTx(ctx, nil)
  	if err != nil {
  		return fmt.Errorf("commonplace: delete: begin: %w", err)
  	}
  	defer func() { _ = tx.Rollback() }()
  	if _, err := tx.ExecContext(ctx, `DELETE FROM entry_fts WHERE entry_id=?`, id); err != nil {
  		return fmt.Errorf("commonplace: delete: fts: %w", err)
  	}
  	if _, err := tx.ExecContext(ctx, `DELETE FROM entry_vec WHERE entry_id=?`, id); err != nil {
  		return fmt.Errorf("commonplace: delete: vec: %w", err)
  	}
  	if _, err := tx.ExecContext(ctx, `DELETE FROM entry WHERE id=?`, id); err != nil {
  		return fmt.Errorf("commonplace: delete: entry: %w", err)
  	}
  	return tx.Commit()
  }

  // ownedEntry loads an entry and verifies caller ownership. Returns
  // ErrNotFound if it isn't visible, ErrForbidden if visible-but-not-owned.
  func (s *Service) ownedEntry(ctx context.Context, org, caller, id string) (Entry, error) {
  	row := s.db.QueryRowContext(ctx, `
  		SELECT id, org, owner, topic, content, visibility, tags, created_at, updated_at
  		FROM entry WHERE id = ? AND org = ?`, id, org)
  	e, err := scanEntry(row)
  	if err != nil {
  		return Entry{}, err
  	}
  	if e.Owner != caller {
  		return Entry{}, ErrForbidden
  	}
  	return e, nil
  }

  func jsonTags(tags []string) (string, error) {
  	if tags == nil {
  		tags = []string{}
  	}
  	b, err := marshalJSON(tags)
  	return string(b), err
  }

  func scanEntry(row *sql.Row) (Entry, error) {
  	var e Entry
  	var tags, created, updated string
  	err := row.Scan(&e.ID, &e.Org, &e.Owner, &e.Topic, &e.Content, &e.Visibility, &tags, &created, &updated)
  	if errors.Is(err, sql.ErrNoRows) {
  		return Entry{}, ErrNotFound
  	}
  	if err != nil {
  		return Entry{}, fmt.Errorf("commonplace: scan entry: %w", err)
  	}
  	e.Tags = parseTags(tags)
  	e.CreatedAt = parseTime(created)
  	e.UpdatedAt = parseTime(updated)
  	return e, nil
  }

  func scanEntryRows(rows *sql.Rows) (Entry, error) {
  	var e Entry
  	var tags, created, updated string
  	if err := rows.Scan(&e.ID, &e.Org, &e.Owner, &e.Topic, &e.Content, &e.Visibility, &tags, &created, &updated); err != nil {
  		return Entry{}, fmt.Errorf("commonplace: scan entry row: %w", err)
  	}
  	e.Tags = parseTags(tags)
  	e.CreatedAt = parseTime(created)
  	e.UpdatedAt = parseTime(updated)
  	return e, nil
  }
  ```
  Add the `marshalJSON` helper to `util.go`:
  ```go
  func marshalJSON(v any) ([]byte, error) { return json.Marshal(v) }
  ```
  Run `go test . -run 'TestGetByID|TestList|TestUpdate|TestDelete'`. Expect PASS.

- [ ] **5.3 — CRUD HTTP handlers test (TDD).** Create `/Users/jacinta/Source/commonplace/handlers_crud_test.go`:
  ```go
  package commonplace

  import (
  	"encoding/json"
  	"net/http"
  	"net/http/httptest"
  	"strings"
  	"testing"
  )

  func authReq(method, path, body, org, subj, scopes string) *http.Request {
  	var r *http.Request
  	if body == "" {
  		r = httptest.NewRequest(method, path, nil)
  	} else {
  		r = httptest.NewRequest(method, path, strings.NewReader(body))
  	}
  	r.Header.Set("X-CWB-Org", org)
  	r.Header.Set("X-CWB-Subject", subj)
  	r.Header.Set("X-CWB-Scopes", scopes)
  	return r
  }

  func TestCRUDHandlers(t *testing.T) {
  	svc := newTestService(t)
  	rw := "knowledge:read knowledge:write"

  	// create
  	rec := httptest.NewRecorder()
  	svc.Handler().ServeHTTP(rec, authReq(http.MethodPost, "/api/knowledge",
  		`{"topic":"t","content":"c","visibility":"org"}`, "acme", "agent:a", rw))
  	if rec.Code != http.StatusCreated {
  		t.Fatalf("create %d: %s", rec.Code, rec.Body.String())
  	}
  	var created Entry
  	_ = json.Unmarshal(rec.Body.Bytes(), &created)

  	// get
  	rec = httptest.NewRecorder()
  	svc.Handler().ServeHTTP(rec, authReq(http.MethodGet, "/api/knowledge/"+created.ID, "", "acme", "agent:a", rw))
  	if rec.Code != http.StatusOK {
  		t.Fatalf("get %d", rec.Code)
  	}

  	// list
  	rec = httptest.NewRecorder()
  	svc.Handler().ServeHTTP(rec, authReq(http.MethodGet, "/api/knowledge", "", "acme", "agent:a", rw))
  	if rec.Code != http.StatusOK {
  		t.Fatalf("list %d", rec.Code)
  	}

  	// patch
  	rec = httptest.NewRecorder()
  	svc.Handler().ServeHTTP(rec, authReq(http.MethodPatch, "/api/knowledge/"+created.ID,
  		`{"content":"updated"}`, "acme", "agent:a", rw))
  	if rec.Code != http.StatusOK {
  		t.Fatalf("patch %d: %s", rec.Code, rec.Body.String())
  	}

  	// delete
  	rec = httptest.NewRecorder()
  	svc.Handler().ServeHTTP(rec, authReq(http.MethodDelete, "/api/knowledge/"+created.ID, "", "acme", "agent:a", rw))
  	if rec.Code != http.StatusNoContent {
  		t.Fatalf("delete %d", rec.Code)
  	}

  	// get after delete -> 404
  	rec = httptest.NewRecorder()
  	svc.Handler().ServeHTTP(rec, authReq(http.MethodGet, "/api/knowledge/"+created.ID, "", "acme", "agent:a", rw))
  	if rec.Code != http.StatusNotFound {
  		t.Fatalf("get-after-delete %d, want 404", rec.Code)
  	}
  }
  ```
  Run `go test . -run TestCRUDHandlers`. Expect FAIL (routes missing).

- [ ] **5.4 — CRUD handlers + route registration.** Add to `handlers.go` `Handler()` (after the search route):
  ```go
  	mux.HandleFunc("GET /api/knowledge", s.handleList)
  	mux.HandleFunc("GET /api/knowledge/{id}", s.handleGet)
  	mux.HandleFunc("PATCH /api/knowledge/{id}", s.handleUpdate)
  	mux.HandleFunc("DELETE /api/knowledge/{id}", s.handleDelete)
  ```
  Add the handlers (note `errors.Is(err, ErrNotFound/ErrForbidden)` status mapping; add `"errors"` import):
  ```go
  func statusFor(err error) (int, string) {
  	switch {
  	case errors.Is(err, ErrNotFound):
  		return http.StatusNotFound, "not found"
  	case errors.Is(err, ErrForbidden):
  		return http.StatusForbidden, "not owner"
  	default:
  		return http.StatusBadRequest, err.Error()
  	}
  }

  func (s *Service) handleGet(w http.ResponseWriter, r *http.Request) {
  	id := identityFromRequest(r)
  	if id.Subject == "" || id.Org == "" {
  		writeErr(w, http.StatusUnauthorized, "missing identity")
  		return
  	}
  	if !id.hasScope(scopeRead) {
  		writeErr(w, http.StatusForbidden, "knowledge:read required")
  		return
  	}
  	e, err := s.Get(r.Context(), id.Org, id.Subject, r.PathValue("id"))
  	if err != nil {
  		code, msg := statusFor(err)
  		writeErr(w, code, msg)
  		return
  	}
  	writeJSON(w, http.StatusOK, e)
  }

  func (s *Service) handleList(w http.ResponseWriter, r *http.Request) {
  	id := identityFromRequest(r)
  	if id.Subject == "" || id.Org == "" {
  		writeErr(w, http.StatusUnauthorized, "missing identity")
  		return
  	}
  	if !id.hasScope(scopeRead) {
  		writeErr(w, http.StatusForbidden, "knowledge:read required")
  		return
  	}
  	list, err := s.List(r.Context(), id.Org, id.Subject)
  	if err != nil {
  		writeErr(w, http.StatusInternalServerError, err.Error())
  		return
  	}
  	if list == nil {
  		list = []Entry{}
  	}
  	writeJSON(w, http.StatusOK, map[string]any{"entries": list})
  }

  type updateBody struct {
  	Topic      *string   `json:"topic"`
  	Content    *string   `json:"content"`
  	Visibility *string   `json:"visibility"`
  	Tags       *[]string `json:"tags"`
  }

  func (s *Service) handleUpdate(w http.ResponseWriter, r *http.Request) {
  	id := identityFromRequest(r)
  	if id.Subject == "" || id.Org == "" {
  		writeErr(w, http.StatusUnauthorized, "missing identity")
  		return
  	}
  	if !id.hasScope(scopeWrite) {
  		writeErr(w, http.StatusForbidden, "knowledge:write required")
  		return
  	}
  	var body updateBody
  	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
  		writeErr(w, http.StatusBadRequest, "invalid json")
  		return
  	}
  	e, err := s.Update(r.Context(), id.Org, id.Subject, r.PathValue("id"), UpdateInput{
  		Topic: body.Topic, Content: body.Content, Visibility: body.Visibility, Tags: body.Tags,
  	})
  	if err != nil {
  		code, msg := statusFor(err)
  		writeErr(w, code, msg)
  		return
  	}
  	writeJSON(w, http.StatusOK, e)
  }

  func (s *Service) handleDelete(w http.ResponseWriter, r *http.Request) {
  	id := identityFromRequest(r)
  	if id.Subject == "" || id.Org == "" {
  		writeErr(w, http.StatusUnauthorized, "missing identity")
  		return
  	}
  	if !id.hasScope(scopeWrite) {
  		writeErr(w, http.StatusForbidden, "knowledge:write required")
  		return
  	}
  	if err := s.Delete(r.Context(), id.Org, id.Subject, r.PathValue("id")); err != nil {
  		code, msg := statusFor(err)
  		writeErr(w, code, msg)
  		return
  	}
  	w.WriteHeader(http.StatusNoContent)
  }
  ```
  Run `go test . -run TestCRUDHandlers`. Expect PASS. NOTE the route-ordering nuance: `GET /api/knowledge/search` (literal) and `GET /api/knowledge/{id}` (wildcard) coexist in Go 1.22+ ServeMux — the literal `search` segment is more specific and wins for `/api/knowledge/search`; an entry id never equals the literal `search`. Confirm `TestGetSearch` still passes alongside `TestCRUDHandlers`.

- [ ] **5.5 — Full suite + commit.**
  ```
  cd /Users/jacinta/Source/commonplace && go build ./... && go test ./... && git add -A && git commit -m "commonplace-mvp: retrieve / list / update (re-embed) / delete

GET /api/knowledge/{id}, GET /api/knowledge, PATCH (sync re-embed on
topic/content change, plan D5), DELETE. Org+visibility scoped reads;
mutations owner-restricted. ErrNotFound/ErrForbidden mapped to 404/403;
no cross-boundary existence oracle.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
  ```

---

## Task 6 — Gateway-identity middleware (shared pattern with ledger)

Promote the inline `identityFromRequest` reader into a proper middleware that injects the verified `Identity` into the request context once, so handlers consume `identityFromContext(r.Context())` instead of re-reading headers. Trust the mTLS-injected `X-CWB-*` (D6); ClusterIP-locked. This is the consistency-with-ledger task: one middleware, applied to the `/api/` subtree, 401 on missing identity centrally, handlers keep only scope checks.

- [ ] **6.1 — Middleware test (TDD).** Create `/Users/jacinta/Source/commonplace/middleware_test.go`:
  ```go
  package commonplace

  import (
  	"net/http"
  	"net/http/httptest"
  	"testing"
  )

  func TestIdentityMiddlewareInjectsContext(t *testing.T) {
  	var got Identity
  	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  		got = identityFromContext(r.Context())
  		w.WriteHeader(http.StatusOK)
  	})
  	h := withIdentity(next)

  	req := httptest.NewRequest(http.MethodGet, "/api/knowledge", nil)
  	req.Header.Set("X-CWB-Org", "acme")
  	req.Header.Set("X-CWB-Subject", "agent:a")
  	req.Header.Set("X-CWB-Kind", "agent")
  	req.Header.Set("X-CWB-Scopes", "knowledge:read knowledge:write")
  	rec := httptest.NewRecorder()
  	h.ServeHTTP(rec, req)

  	if rec.Code != http.StatusOK {
  		t.Fatalf("status %d", rec.Code)
  	}
  	if got.Org != "acme" || got.Subject != "agent:a" || got.Kind != "agent" {
  		t.Errorf("identity = %+v", got)
  	}
  	if !got.hasScope("knowledge:write") {
  		t.Error("missing scope")
  	}
  }

  func TestIdentityMiddlewareRejectsMissing(t *testing.T) {
  	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
  	h := withIdentity(next)
  	req := httptest.NewRequest(http.MethodGet, "/api/knowledge", nil) // no headers
  	rec := httptest.NewRecorder()
  	h.ServeHTTP(rec, req)
  	if rec.Code != http.StatusUnauthorized {
  		t.Fatalf("status %d, want 401", rec.Code)
  	}
  }
  ```
  Run `go test . -run TestIdentityMiddleware`. Expect FAIL: `undefined: withIdentity` / `identityFromContext`.

- [ ] **6.2 — Middleware + context.** Add to `identity.go`:
  ```go
  import "context" // add to existing imports

  type ctxKey string

  const identityCtxKey ctxKey = "cwb-identity"

  // withIdentity reads the gateway-injected X-CWB-* headers, rejects requests
  // with no subject/org (the gateway always sets both for authed requests;
  // their absence means the request didn't transit the gateway), and stashes
  // the verified Identity in context for handlers. Plan D6: trust, don't
  // re-verify — the ClusterIP-locked deploy is what makes this safe.
  func withIdentity(next http.Handler) http.Handler {
  	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  		id := identityFromRequest(r)
  		if id.Subject == "" || id.Org == "" {
  			http.Error(w, `{"error":"missing identity"}`, http.StatusUnauthorized)
  			return
  		}
  		ctx := context.WithValue(r.Context(), identityCtxKey, id)
  		next.ServeHTTP(w, r.WithContext(ctx))
  	})
  }

  // identityFromContext returns the Identity injected by withIdentity.
  func identityFromContext(ctx context.Context) Identity {
  	id, _ := ctx.Value(identityCtxKey).(Identity)
  	return id
  }
  ```
  Run `go test . -run TestIdentityMiddleware`. Expect PASS.

- [ ] **6.3 — Apply middleware; simplify handlers.** Rewrite `handlers.go` `Handler()` so the `/api/` routes are wrapped by `withIdentity` and handlers read from context. Structure:
  ```go
  func (s *Service) Handler() http.Handler {
  	root := http.NewServeMux()
  	root.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
  		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "commonplace"})
  	})

  	api := http.NewServeMux()
  	api.HandleFunc("POST /api/knowledge", s.handleStore)
  	api.HandleFunc("GET /api/knowledge/search", s.handleSearch)
  	api.HandleFunc("GET /api/knowledge", s.handleList)
  	api.HandleFunc("GET /api/knowledge/{id}", s.handleGet)
  	api.HandleFunc("PATCH /api/knowledge/{id}", s.handleUpdate)
  	api.HandleFunc("DELETE /api/knowledge/{id}", s.handleDelete)

  	root.Handle("/api/", withIdentity(api))
  	return root
  }
  ```
  Then change each handler to drop the inline `identityFromRequest` + `Subject==""` 401 block (the middleware now guarantees a valid identity), replacing the first lines with:
  ```go
  	id := identityFromContext(r.Context())
  ```
  and keeping only the scope check. Run the full suite `go test ./...`. Expect PASS — the existing handler tests set the X-CWB-* headers, so they flow through `withIdentity` unchanged, and the no-scope tests still get 403, the no-identity flows still 401 (now from the middleware). Fix any handler that still references the removed local `id` declaration.

- [ ] **6.4 — Doc the ClusterIP precondition.** Append to the top doc-comment of `identity.go` a one-line note: that header-trust is sound only because the deploy (Task 7) locks commonplace to a ClusterIP reachable solely over the mTLS gateway hop — direct pod access would let a caller forge X-CWB-*. (Edit the existing `Identity` doc comment; no behavior change.)

- [ ] **6.5 — Commit.**
  ```
  cd /Users/jacinta/Source/commonplace && go build ./... && go test ./... && git add -A && git commit -m "commonplace-mvp: gateway-identity middleware (X-CWB-* trust)

withIdentity reads the mTLS-injected X-CWB-{Subject,Org,Kind,Scopes},
401s requests that didn't transit the gateway, and injects Identity into
context; handlers consume identityFromContext + keep only scope checks
(knowledge:read/write). Plan D6: trust, don't re-verify — ClusterIP lock
(Task 7) is the safety boundary. Mirrors ledger's middleware shape.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
  ```

---

## Task 7 — Containerfile + k3s manifests (cwb ns) + gateway route

Deploy as a CWB product mirroring herald/ledger: Deployment, ClusterIP Service (behind the gateway, mTLS hop), SQLite+vectors on a PVC, ollama reachability via env, and the gateway `/knowledge` route. The Containerfile from Task 1 is already correct (pure-Go scratch image — no sqlite-vec C extension to bundle, because the vector layer is in-process brute-force cosine over blobs, D1). This task adds the manifests + the gateway route registration.

- [ ] **7.1 — Confirm the image builds.** Run:
  ```
  cd /Users/jacinta/Source/commonplace && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /tmp/commonplace ./cmd/commonplace && /tmp/commonplace -h 2>&1 | head -1 || true
  ```
  Expect: a static binary builds with CGO disabled (confirms the scratch-image Containerfile is valid; ncruces is pure-Go). The binary has no `-h`; it will try to listen — interrupt is fine. The point is the CGO_ENABLED=0 build succeeds. (If a `podman`/`k3s` host is available, also build the image per the Containerfile header; otherwise the binary build is the gating check.)

- [ ] **7.2 — Namespace + PVC.** Create `/Users/jacinta/Source/commonplace/deploy/k3s/00-namespace.yaml`:
  ```yaml
  apiVersion: v1
  kind: Namespace
  metadata:
    name: cwb
    labels:
      name: cwb
  ```
  Create `/Users/jacinta/Source/commonplace/deploy/k3s/10-pvc.yaml`:
  ```yaml
  apiVersion: v1
  kind: PersistentVolumeClaim
  metadata:
    name: commonplace-data
    namespace: cwb
  spec:
    accessModes: ["ReadWriteOnce"]
    storageClassName: local-path
    resources:
      requests:
        storage: 1Gi
  ```

- [ ] **7.3 — Deployment.** Create `/Users/jacinta/Source/commonplace/deploy/k3s/20-deployment.yaml`:
  ```yaml
  apiVersion: apps/v1
  kind: Deployment
  metadata:
    name: commonplace
    namespace: cwb
    labels:
      app: commonplace
  spec:
    replicas: 1
    strategy:
      type: Recreate
    selector:
      matchLabels:
        app: commonplace
    template:
      metadata:
        labels:
          app: commonplace
      spec:
        containers:
          - name: commonplace
            image: localhost/commonplace:dev
            imagePullPolicy: Never
            ports:
              - name: http
                containerPort: 8101
            env:
              - name: COMMONPLACE_ADDR
                value: ":8101"
              - name: COMMONPLACE_DB
                value: "/var/lib/cwb/commonplace.db"
              # Embedding model reachability. Point at the ollama endpoint
              # reachable from the cwb namespace. The MVP default model is
              # nomic-embed-text (dim 768); changing the model is a one-way
              # door requiring a full re-embed (plan D2).
              - name: COMMONPLACE_EMBED_PROVIDER
                value: "ollama"
              - name: COMMONPLACE_EMBED_URL
                value: "http://ollama.cwb.svc.cluster.local:11434"
              - name: COMMONPLACE_EMBED_MODEL
                value: "nomic-embed-text"
              - name: COMMONPLACE_EMBED_DIM
                value: "768"
            volumeMounts:
              - name: data
                mountPath: /var/lib/cwb
            readinessProbe:
              httpGet:
                path: /healthz
                port: http
              initialDelaySeconds: 2
              periodSeconds: 5
            livenessProbe:
              httpGet:
                path: /healthz
                port: http
              initialDelaySeconds: 10
              periodSeconds: 15
        volumes:
          - name: data
            persistentVolumeClaim:
              claimName: commonplace-data
  ```
  NOTE: `COMMONPLACE_EMBED_URL` assumes an `ollama` Service in the `cwb` ns. If ollama lives elsewhere (host, another ns), patch this value at deploy — it's the one external runtime dependency (D2). Healthz needs no embedder reachability (it doesn't embed), so readiness flips green even if ollama is briefly down; store/search will error clearly if ollama is unreachable (matches the OllamaEmbedder error path).

- [ ] **7.4 — Service (ClusterIP).** Create `/Users/jacinta/Source/commonplace/deploy/k3s/30-service.yaml`:
  ```yaml
  apiVersion: v1
  kind: Service
  metadata:
    name: commonplace
    namespace: cwb
    labels:
      app: commonplace
  spec:
    type: ClusterIP
    selector:
      app: commonplace
    ports:
      - name: http
        port: 8101
        targetPort: http
        protocol: TCP
  ```
  ClusterIP (not LoadBalancer/NodePort) is the security boundary for D6: commonplace is reachable only inside the cluster, fronted by the gateway over the mTLS hop.

- [ ] **7.5 — Gateway route note + deploy README.** The gateway routes table lives in interchange's deploy config (`interchange/internal/gateway/gateway.go` reads `Config.Routes` map `"/knowledge" -> "http://commonplace.cwb.svc.cluster.local:8101"`; the gateway package doc-comment already lists commonplace as a fronted service). Create `/Users/jacinta/Source/commonplace/deploy/k3s/README.md`:
  ```markdown
  # commonplace k3s deploy (cwb namespace)

  Mirrors herald/ledger. Build + import the image, then apply:

  ```
  podman build -f cmd/commonplace/Containerfile -t commonplace:dev .
  podman save commonplace:dev | sudo k3s ctr images import -
  kubectl apply -f deploy/k3s/
  ```

  ## Gateway route (interchange side, not in this repo)

  commonplace is fronted by interchange-gateway. Add to the gateway's
  Routes config:

  ```
  "/knowledge": "http://commonplace.cwb.svc.cluster.local:8101"
  ```

  The gateway verifies the herald token and injects X-CWB-* before
  proxying (stripping the `/knowledge` prefix). commonplace trusts those
  headers (plan D6) and is ClusterIP-locked — never expose it directly.

  ## Embedding model

  Requires a reachable ollama serving `nomic-embed-text` (dim 768) at
  `COMMONPLACE_EMBED_URL`. This is the one external runtime dependency.
  Patch the env value if ollama lives outside the cwb namespace.
  Changing the model is a one-way door (full re-embed required).
  ```

- [ ] **7.6 — Commit.**
  ```
  cd /Users/jacinta/Source/commonplace && git add -A && git commit -m "commonplace-mvp: k3s manifests (cwb ns) + gateway route + deploy README

Deployment + ClusterIP Service (mTLS gateway hop, port 8101) + 1Gi PVC
for SQLite+vector blobs, mirroring herald/ledger. Ollama reachability
via COMMONPLACE_EMBED_URL env (nomic-embed-text/768). Pure-Go scratch
image — no C sqlite-vec extension to bundle (vector layer is in-process,
plan D1). Gateway /knowledge route documented for the interchange side.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
  ```

---

## Task 8 — cwb-conformance commonplace layer (CROSS-REPO)

Add a `commonplace` conformance layer to `/Users/jacinta/Source/cwb-conformance` that, through the **real gateway** with **real herald tokens**, stores an entry and proves a **conceptually-related, differently-worded query surfaces it** via semantic search, plus org-scope + visibility isolation. All paths absolute (different repo). This is the live-green DoD half — it requires a deployed commonplace + a reachable embedding model.

**IMPORTANT cross-repo state:** the cwb-conformance repo currently contains ONLY `docs/` + `README.md` — the `internal/{wire,fixtures,target,harness}/`, `conformance/herald/...`, and `cmd/cwb-conform/main.go` described in its design doc are **not yet built** (the herald/gateway layers are the first phase, per the conformance design §8). This task therefore does ONE of two things depending on what exists at execution time:

- **If the harness (`internal/`, `cmd/cwb-conform`) exists:** add the `commonplace` layer as a sibling to `herald`/`gateway` and register it in `cmd/cwb-conform`'s `allLayers`.
- **If the harness does NOT exist yet:** create a self-contained `conformance/commonplace/` package + a minimal target loader so the layer is runnable standalone, structured to slot into the full harness when it lands. Flag the dependency clearly.

The steps below assume the harness exists (the common case once herald/gateway layers land); a fallback note covers the bare-repo case.

- [ ] **8.1 — Survey the harness.** Run:
  ```
  ls -R /Users/jacinta/Source/cwb-conformance/internal /Users/jacinta/Source/cwb-conformance/conformance /Users/jacinta/Source/cwb-conformance/cmd 2>/dev/null; echo "---ALLLAYERS---"; grep -rn "allLayers\|\"herald\"\|\"gateway\"\|\"cairn\"\|\"ledger\"\|\"journey\"" /Users/jacinta/Source/cwb-conformance/cmd/cwb-conform/main.go 2>/dev/null
  ```
  Expect: either the harness tree + an `allLayers` slice listing `herald,gateway,cairn,ledger,journey`, OR empty (bare repo). Decide which branch (8.2a vs 8.2b) applies from this output. Read `internal/wire/http.go`, `internal/wire/mint.go`, `internal/fixtures/org.go`, and `internal/target/target.go` to learn the exact `wire.HTTP` / token-mint / `TestOrg` / `Target` signatures before writing the layer — match them exactly (do NOT invent signatures; the conformance design §3–4 shows the shapes but the code is source of truth).

- [ ] **8.2a — (harness exists) Add the Target paths for commonplace.** In `/Users/jacinta/Source/cwb-conformance/internal/target/target.go`, add a `CommonplacePath string` field (default `/knowledge`) alongside `HeraldPath`/`CairnPath`/`LedgerPath`, and set its default in `load.go` where the other paths default. Match the existing field style exactly.

- [ ] **8.2b — (bare repo fallback) Minimal standalone scaffold.** If the harness is absent, create:
  - `/Users/jacinta/Source/cwb-conformance/go.mod` (`module github.com/CarriedWorldUniverse/cwb-conformance`, `go 1.26.0`) if missing.
  - `/Users/jacinta/Source/cwb-conformance/internal/target/target.go` with the `Target` struct from the design doc §3 **plus** `CommonplacePath`.
  - `/Users/jacinta/Source/cwb-conformance/internal/wire/http.go` with a tiny `Do(method, url, token string, body any) (*http.Response, []byte, error)` raw-HTTP helper through the gateway.
  These are the minimum the commonplace layer needs; leave a `// TODO(harness): replace with the full internal/ harness when the herald/gateway layers land` comment at the top of each and note in the commit that this is provisional scaffolding pending the conformance design's phase-1 build.

- [ ] **8.3 — The commonplace layer.** Create `/Users/jacinta/Source/cwb-conformance/conformance/commonplace/commonplace_test.go`. Structure it as a layer function taking `(*target.Target, *fixtures.TestOrg)` matching the herald/gateway layers (or, in the fallback, taking `(*target.Target)` + minting a token inline). The assertions:
  ```go
  // Package commonplace is the cwb-conformance layer for the knowledge
  // pillar. It drives commonplace through the real interchange-gateway with
  // a real herald token (knowledge:read knowledge:write), proving:
  //   1. store -> a differently-worded conceptual query surfaces the entry
  //      (semantic recall, the commonplace DoD heart);
  //   2. org-scope + visibility isolation (a second org / second agent's
  //      private entry never surfaces).
  // Live-green precondition: a deployed commonplace + a reachable embedding
  // model (ollama nomic-embed-text). Skips with a logged reason if the
  // /knowledge route is absent (commonplace not deployed at the target).
  package commonplace_test
  ```
  Flow (pseudocode to implement against the real `wire`/`fixtures` signatures found in 8.1):
  1. Probe `GET {gateway}{CommonplacePath}/healthz`; if 404/connection-refused, `t.Skip("commonplace not deployed at target")` with the reason logged (per the design's "no silent caps" rule).
  2. Using the `builder` agent's token (has `knowledge:write`; if fixtures grant only repo scopes, mint/provision an agent with `knowledge:read knowledge:write` — extend `fixtures.ProvisionOrg`'s agent scopes to include the knowledge scopes, or add a `knower` agent; match the fixtures pattern):
     - `POST {gateway}{CommonplacePath}/api/knowledge` body `{"topic":"deploying to kubernetes","content":"how to roll out a containerized service to a kubernetes cluster with zero downtime","visibility":"org"}` → expect 201, capture `id`.
     - `POST` a distractor `{"topic":"expense reports","content":"how to file a quarterly expense report","visibility":"org"}` → 201.
  3. **Semantic recall:** `GET {gateway}{CommonplacePath}/api/knowledge/search?q=` + urlencode(`"updating a kubernetes deployment without taking it offline"`) → expect 200; decode `{"hits":[...]}`; assert `hits[0].entry.id == <kubernetes id>` (the reworded query, sharing the concept not the phrasing, surfaces the right entry above the expense distractor). This is the DoD assertion.
  4. **Visibility isolation (same org, second agent):** as the `reader`/second agent (different `X-CWB-Subject`, via its own token), `POST` a `private` entry, then as `builder` search a query matching it → assert it does NOT appear in hits.
  5. **Org isolation:** provision (or reuse) a second org's agent token; search the same `kubernetes` query → assert none of org-A's entries surface (the conformance harness's cross-org check, mirroring the ledger layer's §5d cross-org isolation).
  6. Best-effort cleanup via the entries' `DELETE` (owner-scoped) so reruns don't accrue; tolerate delete failure (teardown is best-effort per the design §4).

- [ ] **8.4 — Register in the CLI's layer list.** In `/Users/jacinta/Source/cwb-conformance/cmd/cwb-conform/main.go`, add `"commonplace"` to `allLayers` (currently `herald,gateway,cairn,ledger,journey`) and wire its run-branch the same way the other layers are dispatched. FLAG in the commit: commonplace joins the selectable layers (`cwb-conform -layers commonplace`), and `-layers all` now includes it; it self-skips when commonplace isn't deployed at the target.

- [ ] **8.5 — Build-green (compile only; live-green is deferred).** Run:
  ```
  cd /Users/jacinta/Source/cwb-conformance && go build ./... && go vet ./...
  ```
  Expect: compiles clean. Do NOT attempt a live run here (it needs the deployed stack + ollama). If the harness was absent and 8.2b's fallback was used, `go build ./...` must still pass on the minimal scaffold.

- [ ] **8.6 — Commit (in the cwb-conformance repo).**
  ```
  cd /Users/jacinta/Source/cwb-conformance && git add -A && git commit -m "commonplace-mvp: add commonplace conformance layer

Drives commonplace through the real gateway with a real herald token:
store an entry, then a differently-worded conceptual query surfaces it
(semantic recall, the commonplace DoD); org-scope + visibility isolation.
Self-skips when /knowledge is absent at the target. Adds CommonplacePath
to Target (default /knowledge) and 'commonplace' to allLayers. Live-green
precondition: deployed commonplace + reachable ollama embedding model.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
  ```

---

## Self-review checklist (run after Task 8)

- [ ] **Placeholder scan:** `grep -rn "TODO\|FIXME\|placeholder\|similar to\|add error handling" /Users/jacinta/Source/commonplace --include='*.go'` returns nothing except the deliberate, named growth-hook comments (the `vec1` scale-up in embed.go; the `entry_edge` block in schema.sql). The single allowed TODO is the `TODO(harness)` marker IF Task 8's bare-repo fallback was used — that's a real cross-repo dependency note, not dead work.
- [ ] **Type/signature consistency:** `Embedder{Embed,Dim}` used identically by store/search/update; `Identity{Subject,Org,Kind,Scopes}` + `hasScope` consistent across middleware + handlers; `Entry`/`StoreInput`/`SearchInput`/`UpdateInput`/`Hit` field names match between service methods, handlers, and tests; the `org+(owner OR visibility='org')` scope predicate is identical in `visibleEntries`, `ftsRank`, `Get`, `List`.
- [ ] **Spec coverage:** every §6 build step → a task (2→T1, 3→T2, 4→T3, 5→T4, 6→T5, 7→T6, 8→T7, 9→T8); every §2 scope item present (entries+visibility T3; neural+hybrid+RRF T4; AI-switchable ollama default T2; all five verbs T3–T5; X-CWB auth T6; deploy T7; substrate-ready seam = Searcher/`search.go` + commented `entry_edge`).
- [ ] **Ladder NAMED not built:** confirm NO `entry_edge` table is created, NO usage-feedback re-rank, NO concept-graph/connectome code exists — only the commented schema block + the "Future" section + the `vec1` growth-hook comment.
- [ ] **sqlite-vec decision RESOLVED:** D1 explicitly chooses pure-Go ncruces driver + brute-force cosine over float32 blobs for MVP, names `ext/vec1` as the drop-in scale-up, states the O(n·dim) tradeoff. No cgo, no upstream sqlite-vec `vec0` dependency.
- [ ] **Optional scaffold-verify:** if a Go toolchain is available, after Task 4 confirm `go test . -run 'TestHybridSearch'` passes (store + brute-force-cosine + RRF + fake embedder → semantic recall green). Note the result.

---

## Future / growth path (NAMED, not built — spec §7)

commonplace MVP is **rung 1** of the learning-memory substrate. The foundation is laid so these grow over the same data + the same `search` seam, with **none of them built in the MVP**:

1. **ANN vector index** — swap the brute-force cosine `Searcher` for the ncruces `ext/vec1` virtual table (`CREATE VIRTUAL TABLE … USING vec1`, registered via `driver.Open(dsn, vec1.Register)`) when corpus size justifies it. Single implementation swap behind the seam; the growth-hook comment in `embed.go` marks the spot.
2. **Usage-feedback weighting** — retrieval starts to *learn* which results were appropriate (re-rank from feedback signals). The first "learning" step.
3. **Concept graph / learning-paths** — activate the commented `entry_edge(from_id,to_id,kind,weight)` table; surface *paths* through related knowledge, not just nearest neighbours.
4. **Connectome-inspired typed-node memory** — neuron-type taxonomy → digital node-types as the storage substrate (north-star research; steer toward, don't bet on).
5. **Async re-embed**, **model-change re-embed migration**, **cross-org knowledge**, **rich human wiki/docs UI**, **WS-bus knowledge-store migration** — each a post-MVP rung.

Each rung ships value AND compounds toward the substrate; never bet the product on the unproven end. Same discipline that parked gRPC.
