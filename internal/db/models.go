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

type AdminUser struct {
	ID           string
	Username     string
	PasswordHash string
	TOTPSecret   *string
	TOTPEnabled  bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type AdminSession struct {
	Token       string
	AdminUserID string
	PendingTOTP bool
	ExpiresAt   time.Time
	CreatedAt   time.Time
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
