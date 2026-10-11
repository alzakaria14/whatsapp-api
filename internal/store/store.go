package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

var ErrNotFound = errors.New("data tidak ditemukan")

type Store struct {
	db *sql.DB
}

type Number struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Phone       string     `json:"phone"`
	SessionJID  string     `json:"-"`
	WebhookURL  string     `json:"webhook_url"`
	Status      string     `json:"status"`
	LastError   string     `json:"last_error,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	ConnectedAt *time.Time `json:"connected_at,omitempty"`
}

type Message struct {
	ID          int64           `json:"id"`
	NumberID    string          `json:"number_id"`
	Direction   string          `json:"direction"`
	RemotePhone string          `json:"remote_phone"`
	RemoteJID   string          `json:"remote_jid"`
	WAMessageID string          `json:"wa_message_id"`
	Type        string          `json:"type"`
	Text        string          `json:"text"`
	Status      string          `json:"status"`
	Error       string          `json:"error,omitempty"`
	RawJSON     json.RawMessage `json:"raw,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

type Delivery struct {
	ID           int64           `json:"id"`
	NumberID     string          `json:"number_id"`
	MessageID    int64           `json:"message_id"`
	WebhookURL   string          `json:"webhook_url"`
	Payload      json.RawMessage `json:"payload"`
	Status       string          `json:"status"`
	Attempts     int             `json:"attempts"`
	ResponseCode int             `json:"response_code"`
	LastError    string          `json:"last_error,omitempty"`
	NextAttempt  time.Time       `json:"next_attempt_at"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

func Open(ctx context.Context, dsn string) (*Store, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("koneksi MySQL gagal: %w", err)
	}
	s := &Store{db: db}
	if err = s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS wa_numbers (
            id CHAR(36) PRIMARY KEY,
            name VARCHAR(100) NOT NULL,
            phone VARCHAR(32) NULL,
            session_jid VARCHAR(191) NULL,
            webhook_url VARCHAR(2048) NOT NULL DEFAULT '',
            status VARCHAR(32) NOT NULL DEFAULT 'disconnected',
            last_error TEXT NOT NULL,
            connected_at DATETIME(6) NULL,
            created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
            updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
			UNIQUE KEY uq_wa_numbers_phone (phone),
			UNIQUE KEY uq_wa_numbers_session (session_jid)
        ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
		`CREATE TABLE IF NOT EXISTS wa_messages (
            id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
            number_id CHAR(36) NOT NULL,
            direction ENUM('inbound','outbound') NOT NULL,
            remote_phone VARCHAR(32) NOT NULL,
            remote_jid VARCHAR(191) NOT NULL,
            wa_message_id VARCHAR(191) NOT NULL,
            type VARCHAR(32) NOT NULL DEFAULT 'text',
            text TEXT NOT NULL,
            status VARCHAR(32) NOT NULL,
            error TEXT NOT NULL,
            raw_json JSON NULL,
            created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
            updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
            CONSTRAINT fk_messages_number FOREIGN KEY (number_id) REFERENCES wa_numbers(id) ON DELETE CASCADE,
            UNIQUE KEY uq_messages_number_wa (number_id, wa_message_id),
            KEY idx_messages_created (created_at),
            KEY idx_messages_number_direction (number_id, direction, created_at)
        ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
		`CREATE TABLE IF NOT EXISTS webhook_deliveries (
            id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
            number_id CHAR(36) NOT NULL,
            message_id BIGINT UNSIGNED NOT NULL,
            webhook_url VARCHAR(2048) NOT NULL,
            payload JSON NOT NULL,
            status ENUM('pending','processing','delivered','failed') NOT NULL DEFAULT 'pending',
            attempts INT UNSIGNED NOT NULL DEFAULT 0,
            response_code INT NOT NULL DEFAULT 0,
            last_error TEXT NOT NULL,
            next_attempt_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
            created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
            updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
            CONSTRAINT fk_deliveries_number FOREIGN KEY (number_id) REFERENCES wa_numbers(id) ON DELETE CASCADE,
            CONSTRAINT fk_deliveries_message FOREIGN KEY (message_id) REFERENCES wa_messages(id) ON DELETE CASCADE,
            KEY idx_deliveries_due (status, next_attempt_at),
            KEY idx_deliveries_number (number_id, created_at)
        ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("migrasi database gagal: %w", err)
		}
	}
	// A process can stop after claiming a delivery but before sending it.
	// Put those rows back in the queue on the next startup.
	if _, err := s.db.ExecContext(ctx, `UPDATE webhook_deliveries SET status = 'failed', next_attempt_at = NOW(6), last_error = 'worker dihentikan sebelum pengiriman selesai' WHERE status = 'processing'`); err != nil {
		return fmt.Errorf("memulihkan antrean webhook: %w", err)
	}
	return nil
}

func (s *Store) CreateNumber(ctx context.Context, n Number) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO wa_numbers (id, name, webhook_url, status, last_error) VALUES (?, ?, ?, 'pairing', '')`, n.ID, n.Name, n.WebhookURL)
	return err
}

func (s *Store) ListNumbers(ctx context.Context) ([]Number, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, COALESCE(phone, ''), COALESCE(session_jid, ''), webhook_url, status, last_error, connected_at, created_at, updated_at FROM wa_numbers ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Number
	for rows.Next() {
		var n Number
		if err := rows.Scan(&n.ID, &n.Name, &n.Phone, &n.SessionJID, &n.WebhookURL, &n.Status, &n.LastError, &n.ConnectedAt, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, n)
	}
	return result, rows.Err()
}

func (s *Store) GetNumber(ctx context.Context, id string) (Number, error) {
	var n Number
	err := s.db.QueryRowContext(ctx, `SELECT id, name, COALESCE(phone, ''), COALESCE(session_jid, ''), webhook_url, status, last_error, connected_at, created_at, updated_at FROM wa_numbers WHERE id = ?`, id).
		Scan(&n.ID, &n.Name, &n.Phone, &n.SessionJID, &n.WebhookURL, &n.Status, &n.LastError, &n.ConnectedAt, &n.CreatedAt, &n.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Number{}, ErrNotFound
	}
	return n, err
}

func (s *Store) FindNumberByPhone(ctx context.Context, phone string) (Number, error) {
	var n Number
	err := s.db.QueryRowContext(ctx, `SELECT id, name, COALESCE(phone, ''), COALESCE(session_jid, ''), webhook_url, status, last_error, connected_at, created_at, updated_at FROM wa_numbers WHERE phone = ?`, phone).
		Scan(&n.ID, &n.Name, &n.Phone, &n.SessionJID, &n.WebhookURL, &n.Status, &n.LastError, &n.ConnectedAt, &n.CreatedAt, &n.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Number{}, ErrNotFound
	}
	return n, err
}

func (s *Store) FindNumberBySession(ctx context.Context, jid string) (Number, error) {
	var n Number
	err := s.db.QueryRowContext(ctx, `SELECT id, name, COALESCE(phone, ''), COALESCE(session_jid, ''), webhook_url, status, last_error, connected_at, created_at, updated_at FROM wa_numbers WHERE session_jid = ?`, jid).
		Scan(&n.ID, &n.Name, &n.Phone, &n.SessionJID, &n.WebhookURL, &n.Status, &n.LastError, &n.ConnectedAt, &n.CreatedAt, &n.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Number{}, ErrNotFound
	}
	return n, err
}

func (s *Store) UpdateNumberWebhook(ctx context.Context, id, name, webhookURL string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE wa_numbers SET name = ?, webhook_url = ? WHERE id = ?`, name, webhookURL, id)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) UpdateNumberStatus(ctx context.Context, id, status, lastError string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE wa_numbers SET status = ?, last_error = ? WHERE id = ?`, status, lastError, id)
	return err
}

func (s *Store) MarkNumberConnected(ctx context.Context, id, phone, jid string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE wa_numbers SET phone = ?, session_jid = ?, status = 'connected', last_error = '', connected_at = NOW(6) WHERE id = ?`, phone, jid, id)
	return err
}

func (s *Store) DeleteNumber(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM wa_numbers WHERE id = ?`, id)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) CreateMessage(ctx context.Context, m Message) (int64, error) {
	result, err := s.db.ExecContext(ctx, `INSERT INTO wa_messages (number_id, direction, remote_phone, remote_jid, wa_message_id, type, text, status, error, raw_json) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.NumberID, m.Direction, m.RemotePhone, m.RemoteJID, m.WAMessageID, m.Type, m.Text, m.Status, m.Error, nullableJSON(m.RawJSON))
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (s *Store) UpdateMessageStatus(ctx context.Context, id int64, status, messageID, errorText string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE wa_messages SET status = ?, wa_message_id = ?, error = ? WHERE id = ?`, status, messageID, errorText, id)
	return err
}

func (s *Store) ListMessages(ctx context.Context, numberID, direction string, limit int) ([]Message, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	query := `SELECT id, number_id, direction, remote_phone, remote_jid, wa_message_id, type, text, status, error, COALESCE(raw_json, JSON_OBJECT()), created_at, updated_at FROM wa_messages WHERE 1=1`
	var args []any
	if numberID != "" {
		query += ` AND number_id = ?`
		args = append(args, numberID)
	}
	if direction != "" {
		query += ` AND direction = ?`
		args = append(args, direction)
	}
	query += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.NumberID, &m.Direction, &m.RemotePhone, &m.RemoteJID, &m.WAMessageID, &m.Type, &m.Text, &m.Status, &m.Error, &m.RawJSON, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, m)
	}
	return result, rows.Err()
}

func (s *Store) QueueDelivery(ctx context.Context, numberID string, messageID int64, url string, payload []byte) (int64, error) {
	result, err := s.db.ExecContext(ctx, `INSERT INTO webhook_deliveries (number_id, message_id, webhook_url, payload, status, last_error) VALUES (?, ?, ?, ?, 'pending', '')`, numberID, messageID, url, payload)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (s *Store) ClaimDueDeliveries(ctx context.Context, limit int) ([]Delivery, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id, number_id, message_id, webhook_url, payload, status, attempts, response_code, last_error, next_attempt_at, created_at, updated_at FROM webhook_deliveries WHERE status IN ('pending','failed') AND next_attempt_at <= NOW(6) ORDER BY next_attempt_at LIMIT ? FOR UPDATE`, limit)
	if err != nil {
		return nil, err
	}
	var result []Delivery
	for rows.Next() {
		var d Delivery
		if err := rows.Scan(&d.ID, &d.NumberID, &d.MessageID, &d.WebhookURL, &d.Payload, &d.Status, &d.Attempts, &d.ResponseCode, &d.LastError, &d.NextAttempt, &d.CreatedAt, &d.UpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		result = append(result, d)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for _, d := range result {
		if _, err := tx.ExecContext(ctx, `UPDATE webhook_deliveries SET status = 'processing' WHERE id = ?`, d.ID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Store) CompleteDelivery(ctx context.Context, id int64, code int) error {
	_, err := s.db.ExecContext(ctx, `UPDATE webhook_deliveries SET status = 'delivered', attempts = attempts + 1, response_code = ?, last_error = '' WHERE id = ?`, code, id)
	return err
}

func (s *Store) FailDelivery(ctx context.Context, id int64, code int, errorText string, next time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE webhook_deliveries SET status = 'failed', attempts = attempts + 1, response_code = ?, last_error = ?, next_attempt_at = ? WHERE id = ?`, code, errorText, next, id)
	return err
}

func (s *Store) StopDelivery(ctx context.Context, id int64, code int, errorText string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE webhook_deliveries SET status = 'failed', attempts = attempts + 1, response_code = ?, last_error = ?, next_attempt_at = '9999-12-31 23:59:59' WHERE id = ?`, code, errorText, id)
	return err
}

func (s *Store) RetryDelivery(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `UPDATE webhook_deliveries SET status = 'pending', attempts = 0, response_code = 0, next_attempt_at = NOW(6), last_error = '' WHERE id = ?`, id)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ListDeliveries(ctx context.Context, numberID string, limit int) ([]Delivery, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	query := `SELECT id, number_id, message_id, webhook_url, payload, status, attempts, response_code, last_error, next_attempt_at, created_at, updated_at FROM webhook_deliveries`
	var args []any
	if numberID != "" {
		query += ` WHERE number_id = ?`
		args = append(args, numberID)
	}
	query += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Delivery
	for rows.Next() {
		var d Delivery
		if err := rows.Scan(&d.ID, &d.NumberID, &d.MessageID, &d.WebhookURL, &d.Payload, &d.Status, &d.Attempts, &d.ResponseCode, &d.LastError, &d.NextAttempt, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, d)
	}
	return result, rows.Err()
}

func nullableJSON(data json.RawMessage) any {
	if len(data) == 0 {
		return nil
	}
	return []byte(data)
}
