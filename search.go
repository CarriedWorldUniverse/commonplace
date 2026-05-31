package commonplace

import (
	"context"
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
			e       Entry
			tags    string
			blob    []byte
			created string
			updated string
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
	for i, sc := range vscored {
		vecRank[i] = sc.id
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
