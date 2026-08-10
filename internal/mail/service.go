package mail

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"kanpic/pkg/identity"
)

type settingsProvider interface {
	Values(context.Context) (map[string]any, error)
}

// directory resolves account identifiers to email addresses. The workbook
// repository already offers this, so mail does not keep its own user table.
type directory interface {
	LookupEmails(context.Context, []string) (map[string]string, error)
}

type Delivery struct {
	ID           string    `json:"id"`
	Event        string    `json:"event"`
	Recipient    string    `json:"recipient"`
	Subject      string    `json:"subject"`
	WorkbookID   string    `json:"workbook_id,omitempty"`
	ActorID      string    `json:"actor_id,omitempty"`
	Status       string    `json:"status"`
	Attempts     int       `json:"attempts"`
	ErrorMessage string    `json:"error_message,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type Summary struct {
	Total  int            `json:"total"`
	Status map[string]int `json:"status"`
}

type Page struct {
	Items   []Delivery `json:"items"`
	Summary Summary    `json:"summary"`
}

type Service struct {
	pool      *pgxpool.Pool
	settings  settingsProvider
	directory directory
	logger    *slog.Logger
	now       func() time.Time
	send      func(context.Context, Config, Message) error
}

func NewService(pool *pgxpool.Pool, settings settingsProvider, users directory, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{pool: pool, settings: settings, directory: users, logger: logger,
		now: func() time.Time { return time.Now().UTC() }, send: Deliver}
}

// SetSender replaces the transport, which lets tests drive the service without
// a real relay.
func (s *Service) SetSender(sender func(context.Context, Config, Message) error) { s.send = sender }

func (s *Service) Config(ctx context.Context) (Config, error) { return readConfig(ctx, s.settings) }

// Notify resolves the recipients and sends in the background so no request
// waits on a mail server. Recipients without an address are skipped quietly.
func (s *Service) Notify(ctx context.Context, notification Notification, actorID string, recipients []string) {
	config, err := s.Config(ctx)
	if err != nil || !config.Enabled || !config.Allows(notification.Event) {
		return
	}
	addresses := s.resolve(ctx, recipients, actorID)
	if len(addresses) == 0 {
		return
	}
	body := notification.Render(config)
	for _, address := range addresses {
		delivery := Delivery{
			ID: identity.New(), Event: notification.Event, Recipient: address, Subject: notification.Subject,
			WorkbookID: notification.WorkbookID, ActorID: actorID, Status: "queued", CreatedAt: s.now(), UpdatedAt: s.now(),
		}
		s.record(ctx, delivery)
		go s.deliver(delivery, config, Message{To: address, Subject: notification.Subject, Body: body})
	}
}

// SendNow delivers immediately and reports the outcome, which is what the
// administrator's test button needs.
func (s *Service) SendNow(ctx context.Context, notification Notification, actorID, recipient string) error {
	config, err := s.Config(ctx)
	if err != nil {
		return err
	}
	if !config.Enabled {
		return ErrDisabled
	}
	delivery := Delivery{ID: identity.New(), Event: notification.Event, Recipient: recipient, Subject: notification.Subject,
		ActorID: actorID, Status: "queued", CreatedAt: s.now(), UpdatedAt: s.now()}
	s.record(ctx, delivery)
	sendContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), config.Timeout+5*time.Second)
	defer cancel()
	err = s.send(sendContext, config, Message{To: recipient, Subject: notification.Subject, Body: notification.Render(config)})
	s.complete(sendContext, delivery, err)
	return err
}

// deliver retries once, because a relay that briefly refuses a connection is
// common and losing the notification is worse than a short wait.
func (s *Service) deliver(delivery Delivery, config Config, message Message) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*config.Timeout+15*time.Second)
	defer cancel()
	var err error
	for attempt := 1; attempt <= 2; attempt++ {
		delivery.Attempts = attempt
		if err = s.send(ctx, config, message); err == nil {
			break
		}
		if attempt == 1 {
			select {
			case <-ctx.Done():
			case <-time.After(2 * time.Second):
			}
		}
	}
	s.complete(ctx, delivery, err)
}

func (s *Service) complete(ctx context.Context, delivery Delivery, cause error) {
	status, message := "sent", ""
	if cause != nil {
		status, message = "failed", cause.Error()
		s.logger.Warn("notification mail failed", "event", delivery.Event, "recipient", delivery.Recipient, "error", cause)
	}
	if s.pool == nil {
		return
	}
	if _, err := s.pool.Exec(ctx, `UPDATE mail_deliveries SET status=$2,attempts=GREATEST(attempts,$3),error_message=$4,updated_at=$5 WHERE id=$1`,
		delivery.ID, status, maxInt(delivery.Attempts, 1), trim(message, 1000), s.now()); err != nil {
		s.logger.Warn("mail delivery status was not recorded", "error", err)
	}
}

func (s *Service) record(ctx context.Context, delivery Delivery) {
	if s.pool == nil {
		return
	}
	var workbookID any
	if strings.TrimSpace(delivery.WorkbookID) != "" {
		workbookID = delivery.WorkbookID
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO mail_deliveries(id,event,recipient,subject,workbook_id,actor_id,status,attempts,created_at,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,'queued',0,$7,$7)`,
		delivery.ID, delivery.Event, delivery.Recipient, trim(delivery.Subject, 300), workbookID, delivery.ActorID, delivery.CreatedAt); err != nil {
		s.logger.Warn("mail delivery was not recorded", "error", err)
	}
}

// resolve turns account identifiers into unique addresses, dropping the actor
// so nobody is told about their own action.
func (s *Service) resolve(ctx context.Context, recipients []string, actorID string) []string {
	wanted := make([]string, 0, len(recipients))
	for _, recipient := range recipients {
		trimmed := strings.TrimSpace(recipient)
		if trimmed == "" || strings.EqualFold(trimmed, strings.TrimSpace(actorID)) {
			continue
		}
		wanted = append(wanted, trimmed)
	}
	if len(wanted) == 0 || s.directory == nil {
		return nil
	}
	emails, err := s.directory.LookupEmails(ctx, wanted)
	if err != nil {
		s.logger.Warn("mail recipients were not resolved", "error", err)
		return nil
	}
	seen, addresses := map[string]struct{}{}, make([]string, 0, len(wanted))
	for _, recipient := range wanted {
		address := strings.TrimSpace(emails[strings.ToLower(recipient)])
		if address == "" && strings.Contains(recipient, "@") {
			// An identifier that is already an address needs no directory entry.
			address = recipient
		}
		if address == "" {
			continue
		}
		key := strings.ToLower(address)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		addresses = append(addresses, address)
	}
	return addresses
}

// Deliveries lists what was sent, newest first, with a status breakdown.
func (s *Service) Deliveries(ctx context.Context, status string, limit int) (Page, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	page := Page{Items: []Delivery{}, Summary: Summary{Status: map[string]int{}}}
	query := `SELECT id::text,event,recipient,subject,coalesce(workbook_id::text,''),actor_id,status,attempts,error_message,created_at,updated_at FROM mail_deliveries`
	args := []any{}
	if trimmed := strings.TrimSpace(status); trimmed != "" {
		args = append(args, trimmed)
		query += ` WHERE status=$1`
	}
	args = append(args, limit)
	rows, err := s.pool.Query(ctx, query+` ORDER BY created_at DESC, id LIMIT $`+itoa(len(args)), args...)
	if err != nil {
		return Page{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var item Delivery
		if err := rows.Scan(&item.ID, &item.Event, &item.Recipient, &item.Subject, &item.WorkbookID, &item.ActorID,
			&item.Status, &item.Attempts, &item.ErrorMessage, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return Page{}, err
		}
		page.Items = append(page.Items, item)
	}
	if err := rows.Err(); err != nil {
		return Page{}, err
	}
	counts, err := s.pool.Query(ctx, `SELECT status, count(*) FROM mail_deliveries GROUP BY 1`)
	if err != nil {
		return Page{}, err
	}
	defer counts.Close()
	for counts.Next() {
		var key string
		var count int
		if err := counts.Scan(&key, &count); err != nil {
			return Page{}, err
		}
		page.Summary.Status[key] = count
		page.Summary.Total += count
	}
	return page, counts.Err()
}

func trim(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func itoa(value int) string {
	digits := ""
	for value > 0 {
		digits = string(rune('0'+value%10)) + digits
		value /= 10
	}
	if digits == "" {
		return "0"
	}
	return digits
}
