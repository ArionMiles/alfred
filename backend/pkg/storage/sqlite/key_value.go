package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/ArionMiles/alfred/pkg/x/errors"
)

// GetKeyValue reads one JSON value from sparse application state.
func (store *Store) GetKeyValue(ctx context.Context, key string) (json.RawMessage, error) {
	const op = "GetKeyValue"

	if strings.TrimSpace(key) == "" {
		return nil, errors.B.Op(op).KindInvalidArgument().Text("key is required").Build()
	}
	var value []byte
	if err := store.db.QueryRowContext(ctx, `
		SELECT value_json FROM key_value WHERE key = @key
	`, sql.Named("key", key)).Scan(&value); err != nil {
		builder := errors.B.Op(op).Text("read application state").Err(err)
		if errors.Is(err, sql.ErrNoRows) {
			builder = builder.KindNotFound()
		}
		return nil, builder.Build()
	}
	return json.RawMessage(value), nil
}

// PutKeyValue atomically creates or replaces one JSON application-state value.
func (store *Store) PutKeyValue(ctx context.Context, key string, value json.RawMessage) error {
	const op = "PutKeyValue"

	if strings.TrimSpace(key) == "" {
		return errors.B.Op(op).KindInvalidArgument().Text("key is required").Build()
	}
	if !json.Valid(value) {
		return errors.B.Op(op).KindInvalidArgument().Text("value is not valid JSON").Build()
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO key_value (key, value_json) VALUES (@key, @value)
		ON CONFLICT (key) DO UPDATE SET value_json = excluded.value_json
	`, sql.Named("key", key), sql.Named("value", []byte(value))); err != nil {
		return errors.B.Op(op).Text("write application state").Err(err).Build()
	}
	return nil
}
