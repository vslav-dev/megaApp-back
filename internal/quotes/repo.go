package quotes

import (
	"context"
	"database/sql"
	"fmt"

	"megaapp-back/internal/platform/sqlite"
)

type Repository struct {
	db    *sql.DB
	write sqlite.WriteDB
}

func NewRepository(read *sql.DB, write sqlite.WriteDB) *Repository {
	return &Repository{db: read, write: write}
}

func (r *Repository) ListRateHistoryRange(ctx context.Context, fromISO string, toISO string) ([]RateHistoryRow, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, dateISO, ratesJson
		FROM moneyRateHistory
		WHERE dateISO >= ? AND dateISO <= ?
		ORDER BY dateISO ASC
	`, fromISO, toISO)
	if err != nil {
		return nil, fmt.Errorf("list rate history range: %w", err)
	}
	defer rows.Close()

	var result []RateHistoryRow
	for rows.Next() {
		var item RateHistoryRow
		if err := rows.Scan(&item.ID, &item.DateISO, &item.RatesJSON); err != nil {
			return nil, fmt.Errorf("scan rate history range row: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rate history range: %w", err)
	}
	return result, nil
}

func (r *Repository) UpsertRateHistoryEntry(ctx context.Context, dateISO string, ratesJSON string) error {
	_, err := r.write.ExecContext(ctx, `
		INSERT INTO moneyRateHistory (dateISO, ratesJson)
		VALUES (?, ?)
		ON CONFLICT(dateISO) DO UPDATE SET ratesJson = excluded.ratesJson
	`, dateISO, ratesJSON)
	if err != nil {
		return fmt.Errorf("upsert rate history entry: %w", err)
	}
	return nil
}

func (r *Repository) ListCurrencyTickers(ctx context.Context) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT DISTINCT ticker
		FROM moneyCurrency
		WHERE ticker != 'USD'
		ORDER BY ticker ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list currency tickers: %w", err)
	}
	defer rows.Close()

	var result []string
	for rows.Next() {
		var ticker string
		if err := rows.Scan(&ticker); err != nil {
			return nil, fmt.Errorf("scan currency ticker: %w", err)
		}
		result = append(result, ticker)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate currency tickers: %w", err)
	}
	return result, nil
}

func (r *Repository) ListOpenAssets(ctx context.Context) ([]OpenAsset, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT a.id, a.ticker, a.type, a.suspendedSince
		FROM moneyAsset a
		JOIN moneyTransaction t
		  ON CAST(json_extract(t.detailsJSON, '$.assetId') AS INTEGER) = a.id
		WHERE t.kind IN ('invest_buy', 'invest_sell')
		  AND t.detailsJSON IS NOT NULL
		  AND json_valid(t.detailsJSON) = 1
		GROUP BY a.id, a.ticker, a.type, a.suspendedSince
		HAVING SUM(
			CASE t.kind
				WHEN 'invest_buy' THEN CAST(json_extract(t.detailsJSON, '$.quantity') AS REAL)
				WHEN 'invest_sell' THEN -CAST(json_extract(t.detailsJSON, '$.quantity') AS REAL)
				ELSE 0
			END
		) > 0
		ORDER BY a.ticker ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list open assets: %w", err)
	}
	defer rows.Close()

	var result []OpenAsset
	for rows.Next() {
		var item OpenAsset
		var suspendedSince sql.NullString
		if err := rows.Scan(&item.ID, &item.Ticker, &item.Type, &suspendedSince); err != nil {
			return nil, fmt.Errorf("scan open asset: %w", err)
		}
		if suspendedSince.Valid {
			item.SuspendedSince = &suspendedSince.String
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate open assets: %w", err)
	}
	return result, nil
}
