package models

import "time"

type Share struct {
	ID              string    `json:"id"`
	Slug            string    `json:"slug"`
	OwnerID         *string   `json:"owner_id"`
	OwnerEmail      string    `json:"owner_email"`
	RecipientEmail  string    `json:"recipient_email"`
	StorageKey      string    `json:"storage_key"`
	FileName        string    `json:"file_name"`
	FileSize        int64     `json:"file_size"`
	CodeHash        string    `json:"code_hash"`
	CodeSalt        string    `json:"code_salt"`
	DownloadedCount int       `json:"downloaded_count"`
	CreatedAt       time.Time `json:"created_at"`
	ExpiresAt       time.Time `json:"expires_at"`
}

type User struct {
	ID    string `json:"sub"`
	Email string `json:"email"`
}
