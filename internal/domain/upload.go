package domain

import "time"

// Upload is a bucket object the retention worker can delete later. Empty
// OwnerID means anonymous, which gets the shorter window.
type Upload struct {
	StorageKey     string    `json:"storage_key"`
	OwnerID        string    `json:"owner_id,omitempty"`
	FileName       string    `json:"file_name"`
	SizeBytes      int64     `json:"size_bytes"`
	CreatedAt      time.Time `json:"created_at"`
	DeleteAttempts int       `json:"delete_attempts"`
}
