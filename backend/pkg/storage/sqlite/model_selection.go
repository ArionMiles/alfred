package sqlite

import (
	"context"
	"database/sql"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

// GetSelectedModel reads the current default without modifying it.
func (store *Store) GetSelectedModel(ctx context.Context) (alfred.ModelRef, error) {
	const op = "GetSelectedModel"
	var model alfred.ModelRef
	err := store.db.QueryRowContext(ctx, `SELECT provider_id, model_id FROM model_selection WHERE id = 1`).Scan(&model.ProviderID, &model.ModelID)
	if err != nil {
		builder := errors.B.Op(op).Text("read selected model").Err(err)
		if errors.Is(err, sql.ErrNoRows) {
			builder = builder.KindNotFound().UserMsg("Choose a model before sending a message.")
		}
		return alfred.ModelRef{}, builder.Build()
	}
	return model, nil
}

// SetSelectedModel atomically replaces the provider/model pair.
func (store *Store) SetSelectedModel(ctx context.Context, model alfred.ModelRef) error {
	const op = "SetSelectedModel"
	if model.ProviderID == "" || model.ModelID == "" {
		return errors.B.Op(op).KindInvalidArgument().Text("model identity is incomplete").Build()
	}
	_, err := store.db.ExecContext(ctx, `
 INSERT INTO model_selection (id, provider_id, model_id) VALUES (1, @provider_id, @model_id)
 ON CONFLICT (id) DO UPDATE SET provider_id = excluded.provider_id, model_id = excluded.model_id
 `, sql.Named("provider_id", model.ProviderID), sql.Named("model_id", model.ModelID))
	if err != nil {
		return errors.B.Op(op).Text("save selected model").Err(err).Build()
	}
	return nil
}

var _ alfred.ModelSelectionStore = (*Store)(nil)
