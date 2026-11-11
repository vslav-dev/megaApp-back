package food

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

type SearchIndexRow struct {
	ID             int64
	Name           string
	Archived       bool
	NameVec        []byte
	DescriptionVec []byte
}

func (r *Repository) GetQueryEmbedding(ctx context.Context, query string) ([]byte, error) {
	row := r.db.QueryRowContext(ctx, `SELECT embedding FROM foodSearchQueryEmbeddings WHERE query = ?`, query)

	var embedding []byte
	if err := row.Scan(&embedding); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get query embedding: %w", err)
	}

	return embedding, nil
}

// RecordQueryEmbeddingHit bumps the cache hit counter for an already-cached query embedding.
// Best-effort and separate from GetQueryEmbedding on purpose: it's just popularity tracking, not
// consistency-critical, so it doesn't need to share a transaction with the read, and it goes
// through the write pool on its own instead of forcing every cache-hit read onto it too.
func (r *Repository) RecordQueryEmbeddingHit(ctx context.Context, query string) error {
	if _, err := r.write.ExecContext(ctx, `
		UPDATE foodSearchQueryEmbeddings
		SET hitCount = hitCount + 1, lastUsedAt = ?
		WHERE query = ?
	`, nowUnixMilli(), query); err != nil {
		return fmt.Errorf("update query embedding usage: %w", err)
	}
	return nil
}

func (r *Repository) SaveQueryEmbedding(ctx context.Context, query string, embedding []byte) error {
	_, err := r.write.ExecContext(ctx, `
		INSERT OR REPLACE INTO foodSearchQueryEmbeddings (query, embedding, hitCount, lastUsedAt, createdAt)
		VALUES (?, ?, 1, ?, ?)
	`, query, embedding, nowUnixMilli(), nowUnixMilli())
	if err != nil {
		return fmt.Errorf("save query embedding: %w", err)
	}
	return nil
}

func (r *Repository) GetSearchIndexRows(ctx context.Context) ([]SearchIndexRow, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, name, archived, nameVec, descriptionVec
		FROM foodCatalogue
		ORDER BY name ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("get search index rows: %w", err)
	}
	defer rows.Close()

	result := make([]SearchIndexRow, 0)
	for rows.Next() {
		var row SearchIndexRow
		if err := rows.Scan(&row.ID, &row.Name, &row.Archived, &row.NameVec, &row.DescriptionVec); err != nil {
			return nil, fmt.Errorf("scan search index row: %w", err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate search index rows: %w", err)
	}
	return result, nil
}

// SearchPickRow is one past choice: the query that was on screen and the product added from it.
type SearchPickRow struct {
	Query       string
	CatalogueID int64
	PickedAt    int64
}

// InsertSearchPick runs in the transaction that creates the diary entry, so a choice exists exactly
// when its entry does.
func (r *Repository) InsertSearchPick(ctx context.Context, tx *sql.Tx, userID int64, query string, catalogueID int64, pickedAt int64) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO foodSearchPicks (usersId, query, foodCatalogueId, pickedAt)
		VALUES (?, ?, ?, ?)
	`, userID, query, catalogueID, pickedAt); err != nil {
		return fmt.Errorf("insert search pick: %w", err)
	}
	return nil
}

// DeleteSearchPicksBefore drops the choices of every user older than the cutoff.
func (r *Repository) DeleteSearchPicksBefore(ctx context.Context, tx *sql.Tx, cutoff int64) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM foodSearchPicks WHERE pickedAt < ?`, cutoff); err != nil {
		return fmt.Errorf("delete old search picks: %w", err)
	}
	return nil
}

// GetRelatedSearchPicks returns the user's choices made on queries related to the given one, not older
// than since: queries that start with it (the range scan over the index) and queries that are its
// beginning (a point lookup per prefix).
func (r *Repository) GetRelatedSearchPicks(ctx context.Context, userID int64, query string, since int64) ([]SearchPickRow, error) {
	args := []any{userID, since, query, query + maxRuneString}
	prefixes := make([]string, 0, utf8.RuneCountInString(query))
	runes := []rune(query)
	for end := 1; end < len(runes); end++ {
		prefixes = append(prefixes, string(runes[:end]))
	}
	prefixSQL := ""
	if len(prefixes) > 0 {
		prefixSQL = `
			UNION ALL
			SELECT query, foodCatalogueId, pickedAt FROM foodSearchPicks
			WHERE usersId = ? AND pickedAt >= ? AND query IN (` + strings.TrimSuffix(strings.Repeat("?,", len(prefixes)), ",") + `)`
		args = append(args, userID, since)
		for _, prefix := range prefixes {
			args = append(args, prefix)
		}
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT query, foodCatalogueId, pickedAt FROM foodSearchPicks
		WHERE usersId = ? AND pickedAt >= ? AND query >= ? AND query < ?`+prefixSQL, args...)
	if err != nil {
		return nil, fmt.Errorf("get search picks: %w", err)
	}
	defer rows.Close()

	result := make([]SearchPickRow, 0)
	for rows.Next() {
		var row SearchPickRow
		if err := rows.Scan(&row.Query, &row.CatalogueID, &row.PickedAt); err != nil {
			return nil, fmt.Errorf("scan search pick: %w", err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate search picks: %w", err)
	}
	return result, nil
}

// maxRuneString sorts after every string that starts with the same text, which makes
// [query, query+maxRuneString) the range of all queries starting with query.
const maxRuneString = "\U0010FFFF"

func nowUnixMilli() int64 {
	return time.Now().UnixMilli()
}
