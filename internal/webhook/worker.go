package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"whatsapp-api/internal/store"
)

type Worker struct {
	store       *store.Store
	client      *http.Client
	secret      []byte
	maxAttempts int
	log         *slog.Logger
	wake        chan struct{}
}

func New(s *store.Store, timeout time.Duration, secret string, maxAttempts int, log *slog.Logger) *Worker {
	return &Worker{
		store: s, client: &http.Client{Timeout: timeout}, secret: []byte(secret),
		maxAttempts: maxAttempts, log: log, wake: make(chan struct{}, 1),
	}
}

func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-w.wake:
		}
		w.process(ctx)
	}
}

func (w *Worker) Wake() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *Worker) process(ctx context.Context) {
	items, err := w.store.ClaimDueDeliveries(ctx, 20)
	if err != nil {
		w.log.Error("mengambil antrean webhook", "error", err)
		return
	}
	for _, item := range items {
		if ctx.Err() != nil {
			return
		}
		w.deliver(ctx, item)
	}
}

func (w *Worker) deliver(ctx context.Context, item store.Delivery) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, item.WebhookURL, bytes.NewReader(item.Payload))
	if err != nil {
		w.markFailed(ctx, item, 0, err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "WhatsApp-Gateway/1.0")
	req.Header.Set("X-Webhook-Delivery", fmt.Sprint(item.ID))
	req.Header.Set("X-Webhook-Signature", "sha256="+signature(w.secret, item.Payload))
	response, err := w.client.Do(req)
	if err != nil {
		w.markFailed(ctx, item, 0, err.Error())
		return
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		if err := w.store.CompleteDelivery(ctx, item.ID, response.StatusCode); err != nil {
			w.log.Error("menyimpan webhook sukses", "error", err, "delivery_id", item.ID)
		}
		return
	}
	w.markFailed(ctx, item, response.StatusCode, response.Status)
}

func (w *Worker) markFailed(ctx context.Context, item store.Delivery, code int, errorText string) {
	attempt := item.Attempts + 1
	var err error
	if attempt >= w.maxAttempts {
		err = w.store.StopDelivery(ctx, item.ID, code, errorText)
	} else {
		delay := time.Minute * time.Duration(1<<min(attempt-1, 8))
		err = w.store.FailDelivery(ctx, item.ID, code, errorText, time.Now().Add(delay))
	}
	if err != nil {
		w.log.Error("menyimpan webhook gagal", "error", err, "delivery_id", item.ID)
	}
}

func signature(secret, payload []byte) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}
