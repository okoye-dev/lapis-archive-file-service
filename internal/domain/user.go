package domain

// User is an authenticated identity, as carried by a verified access token.
type User struct {
	ID    string
	Email string
}
