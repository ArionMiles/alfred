package sqlite

import (
	"context"
	"database/sql"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

// GetCredential returns a copy of the stored secret value.
func (store *Store) GetCredential(ctx context.Context, ref alfred.CredentialRef) ([]byte, error) {
	const op = "GetCredential"

	var value []byte
	err := store.db.QueryRowContext(ctx, `
		SELECT value FROM credentials WHERE ref = @ref
	`, sql.Named("ref", ref)).Scan(&value)
	if err != nil {
		builder := errors.B.Op(op).Text("read credential").Err(err)
		if errors.Is(err, sql.ErrNoRows) {
			builder = builder.KindNotFound().UserMsg("the credential does not exist")
		}
		return nil, builder.Build()
	}
	return value, nil
}

// PutCredential atomically creates or replaces an unencrypted development secret.
func (store *Store) PutCredential(ctx context.Context, ref alfred.CredentialRef, value []byte) error {
	const op = "PutCredential"

	if value == nil {
		value = []byte{}
	}
	_, err := store.db.ExecContext(ctx, `
		INSERT INTO credentials (ref, value) VALUES (@ref, @value)
		ON CONFLICT (ref) DO UPDATE SET value = excluded.value
	`, sql.Named("ref", ref), sql.Named("value", value))
	if err != nil {
		return errors.B.Op(op).Text("write credential").Err(err).Build()
	}
	return nil
}

// DeleteCredential removes a secret; an absent reference is already deleted.
func (store *Store) DeleteCredential(ctx context.Context, ref alfred.CredentialRef) error {
	const op = "DeleteCredential"

	_, err := store.db.ExecContext(ctx, `
		DELETE FROM credentials WHERE ref = @ref
	`, sql.Named("ref", ref))
	if err != nil {
		return errors.B.Op(op).Text("delete credential").Err(err).Build()
	}
	return nil
}

var _ alfred.CredentialProvider = (*Store)(nil)
