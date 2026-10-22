package db

import "time"

type App struct {
	ID              string
	Slug            string
	Name            string
	APIKey          string
	VAPIDPublicKey  string
	VAPIDPrivateKey string
	VAPIDSubject    string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type Subscription struct {
	ID         string
	AppID      string
	UserID     *string
	Endpoint   string
	P256dh     string
	Auth       string
	UserAgent  *string
	CreatedAt  time.Time
	LastSeenAt time.Time
}
