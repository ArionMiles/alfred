package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

// PutSearchSettings saves search metadata independently of model-provider instances.
func (store *Store) PutSearchSettings(ctx context.Context, settings alfred.SearchProviderSettings) error {
	const op = "PutSearchSettings"
	if settings.Backend == "" {
		return errors.B.Op(op).KindInvalidArgument().Text("search backend is required").Build()
	}
	data, err := json.Marshal(settingsRecord{Version: settingsSchemaVersion, Settings: settings.Settings})
	if err != nil {
		return errors.B.Op(op).Err(err).Build()
	}
	_, err = store.db.ExecContext(ctx, `INSERT INTO search_provider_settings (backend,enabled,credential_ref,settings_json)
VALUES (@backend,@enabled,@credential_ref,@settings_json)
ON CONFLICT(backend) DO UPDATE SET enabled=excluded.enabled,credential_ref=excluded.credential_ref,settings_json=excluded.settings_json`,
		sql.Named("backend", settings.Backend), sql.Named("enabled", settings.Enabled), sql.Named("credential_ref", settings.CredentialRef),
		sql.Named("settings_json", string(data)))
	if err != nil {
		return errors.B.Op(op).Text("save search settings").Err(err).Build()
	}
	return nil
}

// GetSearchSettings returns search metadata; an absent backend returns NotFound.
func (store *Store) GetSearchSettings(ctx context.Context, backend string) (alfred.SearchProviderSettings, error) {
	const op = "GetSearchSettings"
	var settings alfred.SearchProviderSettings
	var data []byte
	err := store.db.QueryRowContext(ctx, `SELECT backend,enabled,credential_ref,settings_json FROM search_provider_settings WHERE backend=@backend`,
		sql.Named("backend", backend)).Scan(&settings.Backend, &settings.Enabled, &settings.CredentialRef, &data)
	if err != nil {
		builder := errors.B.Op(op).Text("read search settings").Err(err)
		if errors.Is(err, sql.ErrNoRows) {
			builder = builder.KindNotFound().UserMsg("Search settings have not been saved.")
		}
		return alfred.SearchProviderSettings{}, builder.Build()
	}
	var record settingsRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return alfred.SearchProviderSettings{}, errors.B.Op(op).Err(err).Build()
	}
	if record.Version != settingsSchemaVersion {
		return alfred.SearchProviderSettings{}, errors.B.Op(op).KindFailedPrecondition().Text("unsupported search settings version").Build()
	}
	settings.Settings = record.Settings
	return settings, nil
}

var _ alfred.SearchSettingsStore = (*Store)(nil)
