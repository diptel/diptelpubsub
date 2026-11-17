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
	WebhookURL      *string // optional: incoming WhatsApp replies for this tenant are forwarded here (e.g. an n8n webhook)
	CSEnabled       bool    // muncul di menu bot CS Hub?
	CSMenuLabel     *string // nama tampil di menu bot; fallback ke Name kalau kosong
	CSRefPrefix     *string // mis. "KLR-" — dicocokkan di awal token pesan untuk auto-pilih tenant tanpa lewat menu
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type WANumber struct {
	ID            string
	Label         string
	PhoneNumber   *string
	FonnteToken   string
	Category      string // "otp" | "general"
	Priority      int
	Active        bool
	MinIntervalMs int
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type WAMessage struct {
	ID               string
	AppID            *string
	WANumberID       *string
	Direction        string // "out" | "in"
	Phone            string
	Category         string
	Message          string
	Status           string // "sent" | "failed"
	Error            *string
	ProviderResponse []byte
	CreatedAt        time.Time
}

type EmailMessage struct {
	ID        string
	AppID     string
	ToAddress string
	Subject   string
	Status    string // "sent" | "failed"
	Error     *string
	CreatedAt time.Time
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

// CSSession is one WhatsApp help conversation on the CS number with one customer phone number.
type CSSession struct {
	ID            string
	WANumberID    string
	Phone         string
	AppID         *string  // NULL while Status == "awaiting_service"
	Status        string   // "awaiting_service" | "open" | "closed"
	PendingMenu   []string // app IDs, in the order last shown to the customer
	Rating        *int
	OpenedAt      time.Time
	LastMessageAt time.Time
	ClosedAt      *time.Time
}

type CSMessage struct {
	ID          string
	SessionID   string
	Direction   string // "in" | "out"
	SenderLabel *string
	Message     string
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
