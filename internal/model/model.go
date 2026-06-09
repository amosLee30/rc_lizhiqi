// Package model holds domain types and the notification state machine.
package model

// Status is the internal notification status.
type Status string

const (
	StatusPending    Status = "PENDING"
	StatusDelivering Status = "DELIVERING"
	StatusDelivered  Status = "DELIVERED"
	StatusRetrying   Status = "RETRYING"
	StatusDead       Status = "DEAD"
)

// Coarse is the business-facing three-state status.
type Coarse string

const (
	CoarseAccepted  Coarse = "ACCEPTED"
	CoarseDelivered Coarse = "DELIVERED"
	CoarseFailed    Coarse = "FAILED"
)

// CoarseOf maps an internal status to the business-facing coarse status.
func CoarseOf(s Status) Coarse {
	switch s {
	case StatusDelivered:
		return CoarseDelivered
	case StatusDead:
		return CoarseFailed
	default: // PENDING / DELIVERING / RETRYING
		return CoarseAccepted
	}
}

// IsTerminal reports whether a status is terminal (delivered or dead).
func IsTerminal(s Status) bool {
	return s == StatusDelivered || s == StatusDead
}

// Notification is one accepted notification (the "interaction").
type Notification struct {
	ID               string // = the interaction tracking ID
	IdempotencyKey   string
	SourceSystem     string
	Type             string
	Params           string // raw business input, JSON
	Status           Status
	Attempts         int
	MaxAttempts      int
	NextAttemptAt    int64 // unix seconds
	LeaseOwner       string
	LeaseUntil       int64 // unix seconds
	LastError        string
	LastResponseCode int
	CreatedAt        int64
	UpdatedAt        int64
}

// Event is a status-change record; doubles as the MQ outbox row.
type Event struct {
	ID             string
	NotificationID string
	FromStatus     Status
	ToStatus       Status
	CoarseStatus   Coarse
	AttemptNo      int
	ResponseCode   int
	Error          string
	OccurredAt     int64
	PublishedAt    *int64 // nil = not yet relayed to MQ
}
