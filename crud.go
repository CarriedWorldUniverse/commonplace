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

// DeleteByOrg removes ALL entries for an org (and their fts/vec rows). Used by
// the cross-org wipe (NEX-402). Idempotent: zero entries → (0, nil).
func (s *Service) DeleteByOrg(ctx context.Context, org string) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("commonplace: DeleteByOrg: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM entry_fts WHERE entry_id IN (SELECT id FROM entry WHERE org=?)`, org); err != nil {
		return 0, fmt.Errorf("commonplace: DeleteByOrg: fts: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM entry_vec WHERE entry_id IN (SELECT id FROM entry WHERE org=?)`, org); err != nil {
		return 0, fmt.Errorf("commonplace: DeleteByOrg: vec: %w", err)
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM entry WHERE org=?`, org)
	if err != nil {
		return 0, fmt.Errorf("commonplace: DeleteByOrg: entry: %w", err)
	}
	n, _ := res.RowsAffected()
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commonplace: DeleteByOrg: commit: %w", err)
	}
	return int(n), nil
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
