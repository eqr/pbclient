package migrations

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const defaultCollectionName = "pb_migrations"

// Record stores bookkeeping data for applied migrations inside PocketBase.
type Record struct {
	ID        string `json:"id"`
	AppName   string `json:"appname"`
	Name      string `json:"name"`
	AppliedAt PBTime `json:"applied_at"`
}

// PBTime handles the PocketBase datetime format returned by the API (with a space instead of T).
type PBTime struct {
	time.Time
}

func (t PBTime) MarshalJSON() ([]byte, error) {
	return json.Marshal(t.Time)
}

func (t *PBTime) UnmarshalJSON(data []byte) error {
	str := strings.Trim(string(data), "\"")
	if str == "" || str == "null" {
		t.Time = time.Time{}
		return nil
	}

	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.000Z07:00", "2006-01-02 15:04:05Z07:00"} {
		if parsed, err := time.Parse(layout, str); err == nil {
			t.Time = parsed
			return nil
		}
	}

	return fmt.Errorf("parse time: %s", str)
}

func (t PBTime) After(u time.Time) bool  { return t.Time.After(u) }
func (t PBTime) Before(u time.Time) bool { return t.Time.Before(u) }
func (t PBTime) IsZero() bool            { return t.Time.IsZero() }
