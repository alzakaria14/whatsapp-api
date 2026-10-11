package webhook

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"

	"whatsapp-api/internal/config"
	"whatsapp-api/internal/store"
)

// This test uses the MySQL instance configured in .env and is intentionally
// opt-in: RUN_WEBHOOK_E2E=1 go test ./internal/webhook -run TestWebhookE2E.
// It creates an isolated number/message/delivery and removes all of them when
// the test ends.
func TestWebhookE2E(t *testing.T) {
	if os.Getenv("RUN_WEBHOOK_E2E") != "1" {
		t.Skip("set RUN_WEBHOOK_E2E=1 to run the local MySQL webhook integration test")
	}
	if err := godotenv.Load(filepath.Join("..", "..", ".env")); err != nil {
		t.Fatalf("load .env: %v", err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	db, err := store.Open(ctx, cfg.MySQLDSN)
	if err != nil {
		t.Fatalf("open MySQL: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	payload := []byte(`{"event":"webhook.test","message":{"text":"delivery test"}}`)
	received := make(chan struct{}, 1)
	callback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if got, want := r.Header.Get("X-Webhook-Signature"), "sha256="+signature([]byte(cfg.WebhookSecret), payload); got != want {
			t.Errorf("signature = %q, want %q", got, want)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode payload: %v", err)
		} else if body["event"] != "webhook.test" {
			t.Errorf("event = %v, want webhook.test", body["event"])
		}
		w.WriteHeader(http.StatusNoContent)
		received <- struct{}{}
	}))
	defer callback.Close()

	numberID := uuid.NewString()
	if err := db.CreateNumber(ctx, store.Number{ID: numberID, Name: "E2E webhook test", WebhookURL: callback.URL}); err != nil {
		t.Fatalf("create test number: %v", err)
	}
	t.Cleanup(func() {
		if err := db.DeleteNumber(context.Background(), numberID); err != nil {
			t.Errorf("clean test data: %v", err)
		}
	})
	messageID, err := db.CreateMessage(ctx, store.Message{
		NumberID: numberID, Direction: "inbound", RemotePhone: "6280000000000",
		RemoteJID: "6280000000000@s.whatsapp.net", WAMessageID: uuid.NewString(),
		Type: "text", Text: "delivery test", Status: "received",
	})
	if err != nil {
		t.Fatalf("create test message: %v", err)
	}
	if _, err := db.QueueDelivery(ctx, numberID, messageID, callback.URL, payload); err != nil {
		t.Fatalf("queue delivery: %v", err)
	}

	worker := New(db, 5*time.Second, cfg.WebhookSecret, 3, slog.New(slog.NewTextHandler(io.Discard, nil)))
	worker.process(ctx)
	select {
	case <-received:
	case <-ctx.Done():
		t.Fatal("webhook receiver did not receive the delivery")
	}

	deliveries, err := db.ListDeliveries(ctx, numberID, 10)
	if err != nil {
		t.Fatalf("list deliveries: %v", err)
	}
	if len(deliveries) != 1 || deliveries[0].Status != "delivered" || deliveries[0].ResponseCode != http.StatusNoContent {
		t.Fatalf("unexpected delivery result: %#v", deliveries)
	}
}
