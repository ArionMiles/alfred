package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

// settingsRecord is SQLite's versioned non-secret settings format.
type settingsRecord struct {
	Version  uint32            `json:"version"`
	Settings map[string]string `json:"settings"`
}

const settingsSchemaVersion = 1

// PutProviderInstance atomically creates or replaces service-managed metadata.
func (store *Store) PutProviderInstance(ctx context.Context, instance alfred.ProviderInstance) error {
	const op = "PutProviderInstance"

	if instance.ID == "" || instance.Type == "" {
		return errors.B.Op(op).KindInvalidArgument().Text("invalid provider identity or type").
			UserMsg("the provider configuration is invalid").Build()
	}
	settings, err := json.Marshal(settingsRecord{
		Version: settingsSchemaVersion, Settings: instance.Settings,
	})
	if err != nil {
		return errors.B.Op(op).Text("encode provider settings").Err(err).Build()
	}
	_, err = store.db.ExecContext(ctx, `
		INSERT INTO provider_instances (id, type, name, enabled, credential_ref, settings_json)
		VALUES (@id, @type, @name, @enabled, @credential_ref, @settings_json)
		ON CONFLICT (id) DO UPDATE SET
			type = excluded.type,
			name = excluded.name,
			enabled = excluded.enabled,
			credential_ref = excluded.credential_ref,
			settings_json = excluded.settings_json
	`,
		sql.Named("id", instance.ID),
		sql.Named("type", instance.Type),
		sql.Named("name", instance.Name),
		sql.Named("enabled", instance.Enabled),
		sql.Named("credential_ref", instance.CredentialRef),
		sql.Named("settings_json", string(settings)),
	)
	if err != nil {
		return errors.B.Op(op).Text("save provider instance").Err(err).Build()
	}
	return nil
}

// GetProviderInstance returns saved provider metadata.
func (store *Store) GetProviderInstance(
	ctx context.Context,
	id alfred.ProviderID,
) (alfred.ProviderInstance, error) {
	const op = "GetProviderInstance"

	row := store.db.QueryRowContext(ctx, `
		SELECT id, type, name, enabled, credential_ref, settings_json
		FROM provider_instances WHERE id = @id
	`, sql.Named("id", id))
	instance, err := scanProviderInstance(row)
	if err != nil {
		builder := errors.B.Op(op).Text("read provider instance").Err(err)
		if errors.Is(err, sql.ErrNoRows) {
			builder = builder.KindNotFound().UserMsg("the provider does not exist")
		}
		return alfred.ProviderInstance{}, builder.Build()
	}
	return instance, nil
}

// ListProviderInstances includes disabled instances and orders results by ID.
func (store *Store) ListProviderInstances(ctx context.Context) ([]alfred.ProviderInstance, error) {
	const op = "ListProviderInstances"

	rows, err := store.db.QueryContext(ctx, `
		SELECT id, type, name, enabled, credential_ref, settings_json
		FROM provider_instances ORDER BY id
	`)
	if err != nil {
		return nil, errors.B.Op(op).Text("query provider instances").Err(err).Build()
	}
	defer func() { _ = rows.Close() }()

	instances := make([]alfred.ProviderInstance, 0)
	for rows.Next() {
		instance, err := scanProviderInstance(rows)
		if err != nil {
			return nil, errors.B.Op(op).Text("read provider instance").Err(err).Build()
		}
		instances = append(instances, instance)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.B.Op(op).Text("iterate provider instances").Err(err).Build()
	}
	return instances, nil
}

// DeleteProviderInstance removes metadata without deleting its credential.
func (store *Store) DeleteProviderInstance(ctx context.Context, id alfred.ProviderID) error {
	const op = "DeleteProviderInstance"

	_, err := store.db.ExecContext(ctx, `
		DELETE FROM provider_instances WHERE id = @id
	`, sql.Named("id", id))
	if err != nil {
		return errors.B.Op(op).Text("delete provider instance").Err(err).Build()
	}
	return nil
}

func scanProviderInstance(row interface{ Scan(...any) error }) (alfred.ProviderInstance, error) {
	const op = "scanProviderInstance"

	var instance alfred.ProviderInstance
	var settings []byte
	if err := row.Scan(
		&instance.ID, &instance.Type, &instance.Name,
		&instance.Enabled, &instance.CredentialRef, &settings,
	); err != nil {
		return alfred.ProviderInstance{}, errors.B.Op(op).Text("scan provider instance").Err(err).Build()
	}
	var record settingsRecord
	if err := json.Unmarshal(settings, &record); err != nil {
		return alfred.ProviderInstance{}, errors.B.Op(op).KindInvalidInput().
			Text("decode provider settings").Err(err).Build()
	}
	if record.Version != settingsSchemaVersion {
		return alfred.ProviderInstance{}, errors.B.Op(op).KindInvalidInput().
			Textf("unsupported provider settings version %d", record.Version).Build()
	}
	instance.Settings = record.Settings
	return instance, nil
}

var _ alfred.ProviderInstanceStore = (*Store)(nil)
