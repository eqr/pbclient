package migrations

import "time"

const defaultCollectionName = "pb_migrations"

// Record stores bookkeeping data for applied migrations inside PocketBase.
type Record struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	AppliedAt time.Time `json:"applied_at"`
}
