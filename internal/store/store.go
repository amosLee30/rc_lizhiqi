// Package store is the SQLite-backed persistence + work queue.
//
// The hot `notifications` table doubles as the delivery queue (claimed via
// lease columns). Terminal rows are moved to `notifications_archive` so the
// claim query only ever scans non-terminal work. `notification_events` is the
// status history and the MQ outbox.
package store

import (
	"database/sql"
	"errors"
	"time"

	_ "modernc.org/sqlite"

	"rc_lizhiqi/internal/id"
	"rc_lizhiqi/internal/model"
)

// Store wraps a *sql.DB with notification operations.
type Store struct {
	db  *sql.DB
	now func() int64
}

// Open opens (and migrates) a SQLite database at path.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	// Single connection serializes writes — simplest correct model for the MVP.
	db.SetMaxOpenConns(1)
	s := &Store{db: db, now: func() int64 { return time.Now().Unix() }}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the underlying DB.
func (s *Store) Close() error { return s.db.Close() }

// SetClock overrides the clock (tests only).
func (s *Store) SetClock(now func() int64) { s.now = now }

func (s *Store) migrate() error {
	const schema = `
CREATE TABLE IF NOT EXISTS notifications (
  id               TEXT PRIMARY KEY,
  idempotency_key  TEXT NOT NULL,
  source_system    TEXT NOT NULL,
  type             TEXT NOT NULL,
  params           TEXT NOT NULL,
  status           TEXT NOT NULL,
  attempts         INTEGER NOT NULL DEFAULT 0,
  max_attempts     INTEGER NOT NULL DEFAULT 5,
  next_attempt_at  INTEGER NOT NULL DEFAULT 0,
  lease_owner      TEXT NOT NULL DEFAULT '',
  lease_until      INTEGER NOT NULL DEFAULT 0,
  last_error       TEXT NOT NULL DEFAULT '',
  last_response_code INTEGER NOT NULL DEFAULT 0,
  created_at       INTEGER NOT NULL,
  updated_at       INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_notifications_idem ON notifications(source_system, idempotency_key);
CREATE INDEX IF NOT EXISTS ix_notifications_claim ON notifications(status, next_attempt_at);

CREATE TABLE IF NOT EXISTS notifications_archive (
  id               TEXT PRIMARY KEY,
  idempotency_key  TEXT NOT NULL,
  source_system    TEXT NOT NULL,
  type             TEXT NOT NULL,
  params           TEXT NOT NULL,
  status           TEXT NOT NULL,
  attempts         INTEGER NOT NULL,
  max_attempts     INTEGER NOT NULL,
  last_error       TEXT NOT NULL DEFAULT '',
  last_response_code INTEGER NOT NULL DEFAULT 0,
  created_at       INTEGER NOT NULL,
  updated_at       INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS notification_events (
  id              TEXT PRIMARY KEY,
  notification_id TEXT NOT NULL,
  from_status     TEXT NOT NULL,
  to_status       TEXT NOT NULL,
  coarse_status   TEXT NOT NULL,
  attempt_no      INTEGER NOT NULL,
  response_code   INTEGER NOT NULL DEFAULT 0,
  error           TEXT NOT NULL DEFAULT '',
  occurred_at     INTEGER NOT NULL,
  published_at    INTEGER
);
CREATE INDEX IF NOT EXISTS ix_events_unpublished ON notification_events(published_at);
CREATE INDEX IF NOT EXISTS ix_events_notif ON notification_events(notification_id, occurred_at);
`
	_, err := s.db.Exec(schema)
	return err
}

// ErrConflict is returned when an idempotency key already exists.
var ErrConflict = errors.New("idempotency conflict")

// FindByIdempotency returns the existing notification id for a key, if any.
func (s *Store) FindByIdempotency(source, key string) (string, bool, error) {
	var nid string
	err := s.db.QueryRow(
		`SELECT id FROM notifications WHERE source_system=? AND idempotency_key=?
		 UNION SELECT id FROM notifications_archive WHERE source_system=? AND idempotency_key=?`,
		source, key, source, key).Scan(&nid)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return nid, true, nil
}

// Accept inserts a new PENDING notification and an ACCEPTED event in one tx.
// If the idempotency key already exists, it returns the existing id and false.
func (s *Store) Accept(n *model.Notification) (existingID string, created bool, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", false, err
	}
	defer func() { _ = tx.Rollback() }()

	var prior string
	e := tx.QueryRow(`SELECT id FROM notifications WHERE source_system=? AND idempotency_key=?`,
		n.SourceSystem, n.IdempotencyKey).Scan(&prior)
	if e == nil {
		return prior, false, nil
	} else if !errors.Is(e, sql.ErrNoRows) {
		return "", false, e
	}

	now := s.now()
	n.Status = model.StatusPending
	n.NextAttemptAt = now
	n.CreatedAt, n.UpdatedAt = now, now
	if _, err = tx.Exec(`INSERT INTO notifications
		(id, idempotency_key, source_system, type, params, status, attempts, max_attempts, next_attempt_at, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		n.ID, n.IdempotencyKey, n.SourceSystem, n.Type, n.Params, n.Status, n.Attempts, n.MaxAttempts, n.NextAttemptAt, n.CreatedAt, n.UpdatedAt); err != nil {
		return "", false, err
	}
	if err = appendEvent(tx, n.ID, "", model.StatusPending, 0, 0, "", now); err != nil {
		return "", false, err
	}
	if err = tx.Commit(); err != nil {
		return "", false, err
	}
	return n.ID, true, nil
}

// ClaimBatch atomically leases up to limit claimable notifications to owner.
// Claimable = next_attempt_at<=now AND (PENDING/RETRYING, or DELIVERING with an
// expired lease — the reaper folded into the claim). attempts is incremented on claim.
func (s *Store) ClaimBatch(owner string, leaseSeconds, limit int) ([]model.Notification, error) {
	now := s.now()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.Query(`SELECT id FROM notifications
		WHERE next_attempt_at<=? AND (
		  status IN ('PENDING','RETRYING') OR (status='DELIVERING' AND lease_until<?))
		ORDER BY next_attempt_at LIMIT ?`, now, now, limit)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var nid string
		if err := rows.Scan(&nid); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, nid)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()

	leaseUntil := now + int64(leaseSeconds)
	var claimed []model.Notification
	for _, nid := range ids {
		res, err := tx.Exec(`UPDATE notifications
			SET status='DELIVERING', attempts=attempts+1, lease_owner=?, lease_until=?, updated_at=?
			WHERE id=? AND next_attempt_at<=? AND (
			  status IN ('PENDING','RETRYING') OR (status='DELIVERING' AND lease_until<?))`,
			owner, leaseUntil, now, nid, now, now)
		if err != nil {
			return nil, err
		}
		if aff, _ := res.RowsAffected(); aff != 1 {
			continue
		}
		var n model.Notification
		if err := scanNotification(tx.QueryRow(selectCols+` FROM notifications WHERE id=?`, nid), &n); err != nil {
			return nil, err
		}
		claimed = append(claimed, n)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return claimed, nil
}

// MarkDelivered transitions a notification to DELIVERED and records an event.
func (s *Store) MarkDelivered(n *model.Notification, code int) error {
	return s.transition(n, model.StatusDelivered, n.NextAttemptAt, "", code)
}

// MarkRetry schedules a retry: status RETRYING with a future next_attempt_at.
func (s *Store) MarkRetry(n *model.Notification, nextAttemptAt int64, errMsg string, code int) error {
	return s.transition(n, model.StatusRetrying, nextAttemptAt, errMsg, code)
}

// MarkDead moves a notification to the DEAD terminal state.
func (s *Store) MarkDead(n *model.Notification, errMsg string, code int) error {
	return s.transition(n, model.StatusDead, n.NextAttemptAt, errMsg, code)
}

func (s *Store) transition(n *model.Notification, to model.Status, nextAt int64, errMsg string, code int) error {
	now := s.now()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	from := n.Status
	if _, err = tx.Exec(`UPDATE notifications
		SET status=?, next_attempt_at=?, last_error=?, last_response_code=?, lease_owner='', lease_until=0, updated_at=?
		WHERE id=?`, to, nextAt, errMsg, code, now, n.ID); err != nil {
		return err
	}
	if err = appendEvent(tx, n.ID, from, to, n.Attempts, code, errMsg, now); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	n.Status, n.NextAttemptAt, n.LastError, n.LastResponseCode, n.UpdatedAt = to, nextAt, errMsg, code, now
	return nil
}

// Get returns a notification by id, searching the hot table then the archive.
func (s *Store) Get(nid string) (*model.Notification, error) {
	var n model.Notification
	err := scanNotification(s.db.QueryRow(selectCols+` FROM notifications WHERE id=?`, nid), &n)
	if err == nil {
		return &n, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	err = scanNotification(s.db.QueryRow(archiveCols+` FROM notifications_archive WHERE id=?`, nid), &n)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &n, nil
}

// Events returns the status history for a notification (oldest first).
func (s *Store) Events(nid string) ([]model.Event, error) {
	rows, err := s.db.Query(`SELECT id, notification_id, from_status, to_status, coarse_status,
		attempt_no, response_code, error, occurred_at, published_at
		FROM notification_events WHERE notification_id=? ORDER BY occurred_at, id`, nid)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.Event
	for rows.Next() {
		var e model.Event
		if err := rows.Scan(&e.ID, &e.NotificationID, &e.FromStatus, &e.ToStatus, &e.CoarseStatus,
			&e.AttemptNo, &e.ResponseCode, &e.Error, &e.OccurredAt, &e.PublishedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ListDead returns up to limit DEAD notifications (from hot + archive).
func (s *Store) ListDead(limit int) ([]model.Notification, error) {
	rows, err := s.db.Query(selectCols+` FROM notifications WHERE status='DEAD'
		UNION ALL `+archiveCols+` FROM notifications_archive WHERE status='DEAD'
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.Notification
	for rows.Next() {
		var n model.Notification
		if err := rows.Scan(&n.ID, &n.IdempotencyKey, &n.SourceSystem, &n.Type, &n.Params, &n.Status,
			&n.Attempts, &n.MaxAttempts, &n.NextAttemptAt, &n.LeaseOwner, &n.LeaseUntil,
			&n.LastError, &n.LastResponseCode, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// Replay re-queues a DEAD notification: bring it back from archive (if needed),
// reset attempts/lease and set status PENDING. Returns false if not found/not DEAD.
func (s *Store) Replay(nid string) (bool, error) {
	now := s.now()
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()

	// If archived, restore the row into the hot table first.
	var n model.Notification
	e := scanNotification(tx.QueryRow(selectCols+` FROM notifications WHERE id=?`, nid), &n)
	if errors.Is(e, sql.ErrNoRows) {
		if err := scanNotification(tx.QueryRow(archiveCols+` FROM notifications_archive WHERE id=?`, nid), &n); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return false, nil
			}
			return false, err
		}
		if _, err := tx.Exec(`INSERT INTO notifications
			(id, idempotency_key, source_system, type, params, status, attempts, max_attempts, next_attempt_at, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
			n.ID, n.IdempotencyKey, n.SourceSystem, n.Type, n.Params, n.Status, n.Attempts, n.MaxAttempts, 0, n.CreatedAt, now); err != nil {
			return false, err
		}
		if _, err := tx.Exec(`DELETE FROM notifications_archive WHERE id=?`, nid); err != nil {
			return false, err
		}
	} else if e != nil {
		return false, e
	}
	if n.Status != model.StatusDead {
		return false, nil
	}
	if _, err := tx.Exec(`UPDATE notifications
		SET status='PENDING', attempts=0, next_attempt_at=?, lease_owner='', lease_until=0, last_error='', updated_at=?
		WHERE id=?`, now, now, nid); err != nil {
		return false, err
	}
	if err := appendEvent(tx, nid, model.StatusDead, model.StatusPending, 0, 0, "replay", now); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// ArchiveTerminal moves up to limit terminal rows out of the hot table.
// Returns the number archived.
func (s *Store) ArchiveTerminal(limit int) (int, error) {
	now := s.now()
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.Query(`SELECT id FROM notifications WHERE status IN ('DELIVERED','DEAD') LIMIT ?`, limit)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var nid string
		if err := rows.Scan(&nid); err != nil {
			_ = rows.Close()
			return 0, err
		}
		ids = append(ids, nid)
	}
	_ = rows.Close()

	for _, nid := range ids {
		if _, err := tx.Exec(`INSERT OR REPLACE INTO notifications_archive
			(id, idempotency_key, source_system, type, params, status, attempts, max_attempts, last_error, last_response_code, created_at, updated_at)
			SELECT id, idempotency_key, source_system, type, params, status, attempts, max_attempts, last_error, last_response_code, created_at, ?
			FROM notifications WHERE id=?`, now, nid); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`DELETE FROM notifications WHERE id=?`, nid); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(ids), nil
}

// UnpublishedEvents returns up to limit events not yet relayed to MQ.
func (s *Store) UnpublishedEvents(limit int) ([]model.Event, error) {
	rows, err := s.db.Query(`SELECT id, notification_id, from_status, to_status, coarse_status,
		attempt_no, response_code, error, occurred_at, published_at
		FROM notification_events WHERE published_at IS NULL ORDER BY occurred_at, id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.Event
	for rows.Next() {
		var e model.Event
		if err := rows.Scan(&e.ID, &e.NotificationID, &e.FromStatus, &e.ToStatus, &e.CoarseStatus,
			&e.AttemptNo, &e.ResponseCode, &e.Error, &e.OccurredAt, &e.PublishedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// MarkPublished stamps an event as relayed to MQ.
func (s *Store) MarkPublished(eventID string) error {
	_, err := s.db.Exec(`UPDATE notification_events SET published_at=? WHERE id=?`, s.now(), eventID)
	return err
}

const selectCols = `SELECT id, idempotency_key, source_system, type, params, status, attempts, max_attempts,
	next_attempt_at, lease_owner, lease_until, last_error, last_response_code, created_at, updated_at`

// archive rows have no queue columns; we select constants to match the scan shape.
const archiveCols = `SELECT id, idempotency_key, source_system, type, params, status, attempts, max_attempts,
	0 AS next_attempt_at, '' AS lease_owner, 0 AS lease_until, last_error, last_response_code, created_at, updated_at`

type rowScanner interface{ Scan(dest ...any) error }

func scanNotification(r rowScanner, n *model.Notification) error {
	return r.Scan(&n.ID, &n.IdempotencyKey, &n.SourceSystem, &n.Type, &n.Params, &n.Status,
		&n.Attempts, &n.MaxAttempts, &n.NextAttemptAt, &n.LeaseOwner, &n.LeaseUntil,
		&n.LastError, &n.LastResponseCode, &n.CreatedAt, &n.UpdatedAt)
}

func appendEvent(tx *sql.Tx, nid string, from, to model.Status, attempt, code int, errMsg string, now int64) error {
	_, err := tx.Exec(`INSERT INTO notification_events
		(id, notification_id, from_status, to_status, coarse_status, attempt_no, response_code, error, occurred_at, published_at)
		VALUES (?,?,?,?,?,?,?,?,?,NULL)`,
		id.New(), nid, from, to, model.CoarseOf(to), attempt, code, errMsg, now)
	return err
}
