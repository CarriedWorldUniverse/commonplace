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
