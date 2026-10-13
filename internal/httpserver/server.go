package httpserver

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/skip2/go-qrcode"

	"whatsapp-api/internal/config"
	"whatsapp-api/internal/store"
	"whatsapp-api/internal/webhook"
	"whatsapp-api/internal/whatsapp"
)

//go:embed web/*
var webFiles embed.FS

type Server struct {
	cfg    config.Config
	store  *store.Store
	wa     *whatsapp.Manager
	worker *webhook.Worker
	log    *slog.Logger
}

func New(cfg config.Config, data *store.Store, wa *whatsapp.Manager, worker *webhook.Worker, log *slog.Logger) http.Handler {
	s := &Server{cfg: cfg, store: data, wa: wa, worker: worker, log: log}
	mux := http.NewServeMux()

	assets, _ := fs.Sub(webFiles, "web")
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(assets))))
	mux.Handle("GET /", s.dashboardAuth(http.HandlerFunc(s.index)))
	mux.Handle("GET /dashboard/api/numbers", s.dashboardAuth(http.HandlerFunc(s.listNumbers)))
	mux.Handle("POST /dashboard/api/numbers", s.dashboardAuth(http.HandlerFunc(s.createNumber)))
	mux.Handle("PATCH /dashboard/api/numbers/{id}", s.dashboardAuth(http.HandlerFunc(s.updateNumber)))
	mux.Handle("DELETE /dashboard/api/numbers/{id}", s.dashboardAuth(http.HandlerFunc(s.deleteNumber)))
	mux.Handle("POST /dashboard/api/numbers/{id}/pair", s.dashboardAuth(http.HandlerFunc(s.restartPairing)))
	mux.Handle("GET /dashboard/api/numbers/{id}/qr", s.dashboardAuth(http.HandlerFunc(s.qrCode)))
	mux.Handle("GET /dashboard/api/messages", s.dashboardAuth(http.HandlerFunc(s.dashboardMessages)))
	mux.Handle("POST /dashboard/api/messages", s.dashboardAuth(http.HandlerFunc(s.sendMessage)))
	mux.Handle("GET /dashboard/api/webhooks", s.dashboardAuth(http.HandlerFunc(s.dashboardWebhooks)))
	mux.Handle("POST /dashboard/api/webhooks/{id}/retry", s.dashboardAuth(http.HandlerFunc(s.retryWebhook)))

	mux.Handle("GET /api/v1/numbers", s.apiAuth(http.HandlerFunc(s.apiNumbers)))
	mux.Handle("GET /api/v1/messages", s.apiAuth(http.HandlerFunc(s.apiMessages)))
	mux.Handle("POST /api/v1/messages", s.apiAuth(http.HandlerFunc(s.sendMessage)))
	mux.HandleFunc("GET /health", s.health)
	return s.securityHeaders(s.accessLog(mux))
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data, err := webFiles.ReadFile("web/index.html")
	if err != nil {
		http.Error(w, "dashboard tidak tersedia", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

func (s *Server) listNumbers(w http.ResponseWriter, r *http.Request) {
	numbers, err := s.store.ListNumbers(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": nonNil(numbers)})
}

func (s *Server) createNumber(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name       string `json:"name"`
		WebhookURL string `json:"webhook_url"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.WebhookURL = strings.TrimSpace(input.WebhookURL)
	if input.Name == "" || len(input.Name) > 100 {
		writeError(w, http.StatusUnprocessableEntity, "Nama wajib diisi (maksimal 100 karakter)")
		return
	}
	if input.WebhookURL != "" && !validHTTPURL(input.WebhookURL) {
		writeError(w, http.StatusUnprocessableEntity, "URL webhook harus berupa http:// atau https:// yang valid")
		return
	}
	number := store.Number{ID: uuid.NewString(), Name: input.Name, WebhookURL: input.WebhookURL}
	if err := s.store.CreateNumber(r.Context(), number); err != nil {
		s.internalError(w, err)
		return
	}
	if err := s.wa.StartPairing(r.Context(), number.ID, s.cfg.LogLevel); err != nil {
		_ = s.store.UpdateNumberStatus(r.Context(), number.ID, "disconnected", err.Error())
		writeJSON(w, http.StatusCreated, map[string]any{"data": number, "warning": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"data": number})
}

func (s *Server) updateNumber(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name       string `json:"name"`
		WebhookURL string `json:"webhook_url"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Name, input.WebhookURL = strings.TrimSpace(input.Name), strings.TrimSpace(input.WebhookURL)
	if input.Name == "" || len(input.Name) > 100 {
		writeError(w, http.StatusUnprocessableEntity, "Nama wajib diisi (maksimal 100 karakter)")
		return
	}
	if input.WebhookURL != "" && !validHTTPURL(input.WebhookURL) {
		writeError(w, http.StatusUnprocessableEntity, "URL webhook tidak valid")
		return
	}
	if err := s.store.UpdateNumberWebhook(r.Context(), r.PathValue("id"), input.Name, input.WebhookURL); err != nil {
		s.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "Pengaturan tersimpan"})
}

func (s *Server) deleteNumber(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.wa.Logout(r.Context(), id); err != nil {
		s.log.Warn("logout sebelum hapus gagal", "error", err, "number_id", id)
	}
	if err := s.store.DeleteNumber(r.Context(), id); err != nil {
		s.storeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) restartPairing(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	number, err := s.store.GetNumber(r.Context(), id)
	if err != nil {
		s.storeError(w, err)
		return
	}
	if number.Status == "connected" || number.Status == "reconnecting" {
		writeError(w, http.StatusConflict, "Sesi masih aktif atau sedang menyambung ulang; QR tidak diperlukan")
		return
	}
	if number.Status != "disconnected" && number.Status != "logged_out" && number.Status != "pairing" {
		writeError(w, http.StatusConflict, "Sesi tidak dapat dipairing ulang; periksa status dan log gateway")
		return
	}
	if err := s.wa.RestartPairing(r.Context(), id, s.cfg.LogLevel); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"message": "Pairing dimulai"})
}

func (s *Server) qrCode(w http.ResponseWriter, r *http.Request) {
	code, ok := s.wa.QRCode(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "QR belum tersedia atau pairing sudah selesai")
		return
	}
	png, err := qrcode.Encode(code, qrcode.Medium, 320)
	if err != nil {
		s.internalError(w, err)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(png)
}

func (s *Server) dashboardMessages(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListMessages(r.Context(), r.URL.Query().Get("number_id"), validDirection(r.URL.Query().Get("direction")), queryLimit(r, 100))
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": nonNil(items)})
}

func (s *Server) dashboardWebhooks(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListDeliveries(r.Context(), r.URL.Query().Get("number_id"), queryLimit(r, 100))
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": nonNil(items)})
}

func (s *Server) retryWebhook(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "ID tidak valid")
		return
	}
	if err := s.store.RetryDelivery(r.Context(), id); err != nil {
		s.storeError(w, err)
		return
	}
	s.worker.Wake()
	writeJSON(w, http.StatusAccepted, map[string]any{"message": "Webhook dimasukkan kembali ke antrean"})
}

func (s *Server) apiNumbers(w http.ResponseWriter, r *http.Request) { s.listNumbers(w, r) }

func (s *Server) apiMessages(w http.ResponseWriter, r *http.Request) {
	numberID := ""
	if from := normalizePhone(r.URL.Query().Get("from")); from != "" {
		number, err := s.store.FindNumberByPhone(r.Context(), from)
		if err != nil {
			s.storeError(w, err)
			return
		}
		numberID = number.ID
	}
	items, err := s.store.ListMessages(r.Context(), numberID, validDirection(r.URL.Query().Get("direction")), queryLimit(r, 50))
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": nonNil(items)})
}

func (s *Server) sendMessage(w http.ResponseWriter, r *http.Request) {
	var input struct {
		From string `json:"from"`
		To   string `json:"to"`
		Text string `json:"text"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.From, input.To, input.Text = normalizePhone(input.From), normalizeRecipient(input.To), strings.TrimSpace(input.Text)
	if input.From == "" || input.To == "" || input.Text == "" {
		writeError(w, http.StatusUnprocessableEntity, "from, to, dan text wajib diisi")
		return
	}
	if len(input.Text) > 65535 {
		writeError(w, http.StatusUnprocessableEntity, "Pesan terlalu panjang")
		return
	}
	number, err := s.store.FindNumberByPhone(r.Context(), input.From)
	if err != nil {
		s.storeError(w, err)
		return
	}
	localID := uuid.NewString()
	message := store.Message{NumberID: number.ID, Direction: "outbound", RemotePhone: input.To, RemoteJID: input.To + "@s.whatsapp.net", WAMessageID: localID, Type: "text", Text: input.Text, Status: "pending"}
	id, err := s.store.CreateMessage(r.Context(), message)
	if err != nil {
		s.internalError(w, err)
		return
	}
	ctx, cancel := timeContext(r, 30*time.Second)
	defer cancel()
	waID, err := s.wa.SendText(ctx, number.ID, input.To, input.Text)
	if err != nil {
		_ = s.store.UpdateMessageStatus(r.Context(), id, "failed", localID, err.Error())
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "message_id": id})
		return
	}
	if err := s.store.UpdateMessageStatus(r.Context(), id, "sent", waID, ""); err != nil {
		s.log.Error("update pesan terkirim", "error", err)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"data": map[string]any{"id": id, "wa_message_id": waID, "status": "sent"}})
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (s *Server) dashboardAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		validUser := subtle.ConstantTimeCompare([]byte(user), []byte(s.cfg.DashboardUser)) == 1
		validPass := subtle.ConstantTimeCompare([]byte(pass), []byte(s.cfg.DashboardPassword)) == 1
		if !ok || !validUser || !validPass {
			w.Header().Set("WWW-Authenticate", `Basic realm="WhatsApp Gateway", charset="UTF-8"`)
			http.Error(w, "Autentikasi dashboard diperlukan", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) apiAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-API-Key")
		if key == "" && strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			key = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		}
		if subtle.ConstantTimeCompare([]byte(key), []byte(s.cfg.APIKey)) != 1 {
			writeError(w, http.StatusUnauthorized, "API key tidak valid")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		s.log.Info("http", "method", r.Method, "path", r.URL.Path, "duration", time.Since(start))
	})
}

func (s *Server) storeError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	s.internalError(w, err)
}

func (s *Server) internalError(w http.ResponseWriter, err error) {
	s.log.Error("request gagal", "error", err)
	writeError(w, http.StatusInternalServerError, "Terjadi kesalahan internal")
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "JSON tidak valid: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"error": message})
}

func queryLimit(r *http.Request, fallback int) int {
	value, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || value < 1 || value > 200 {
		return fallback
	}
	return value
}
func validDirection(value string) string {
	if value == "inbound" || value == "outbound" {
		return value
	}
	return ""
}
func validHTTPURL(value string) bool {
	parsed, err := url.ParseRequestURI(value)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
}
func normalizePhone(value string) string {
	var out strings.Builder
	for _, c := range value {
		if c >= '0' && c <= '9' {
			out.WriteRune(c)
		}
	}
	return out.String()
}
func normalizeRecipient(value string) string {
	phone := normalizePhone(value)
	if strings.HasPrefix(phone, "0") {
		return "62" + strings.TrimPrefix(phone, "0")
	}
	return phone
}
func nonNil[T any](items []T) []T {
	if items == nil {
		return []T{}
	}
	return items
}

func timeContext(r *http.Request, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), timeout)
}
