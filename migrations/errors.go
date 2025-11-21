package migrations

import "errors"

var (
	ErrMigrationFailed    = errors.New("migration failed")
	ErrDuplicateMigration = errors.New("duplicate migration")
	ErrMigrationNotFound  = errors.New("migration not found")
	ErrCollectionNotFound = errors.New("collection not found")
)
