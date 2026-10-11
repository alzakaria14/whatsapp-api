package whatsapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"go.mau.fi/whatsmeow"
	waCompanionReg "go.mau.fi/whatsmeow/proto/waCompanionReg"
	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	waStore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
	_ "modernc.org/sqlite"

	appstore "whatsapp-api/internal/store"
)

var (
	ErrNotConnected = errors.New("nomor WhatsApp tidak terhubung")
	ErrPairing      = errors.New("nomor sedang dalam proses pairing")
)

type Manager struct {
	store     *appstore.Store
	container *sqlstore.Container
	log       *slog.Logger
	mu        sync.RWMutex
	clients   map[string]*whatsmeow.Client
	qrCodes   map[string]string
	cancels   map[string]context.CancelFunc
}

func New(ctx context.Context, sessionDSN, logLevel string, appStore *appstore.Store, logger *slog.Logger) (*Manager, error) {
	configureChromeIdentity()
	if strings.HasPrefix(sessionDSN, "file:") {
		path := strings.TrimPrefix(strings.Split(sessionDSN, "?")[0], "file:")
		if dir := filepath.Dir(path); dir != "." {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return nil, err
			}
		}
	}
	container, err := sqlstore.New(ctx, "sqlite", sessionDSN, waLog.Stdout("WA-DB", logLevel, true))
	if err != nil {
		return nil, fmt.Errorf("membuka session store WhatsApp: %w", err)
	}
	m := &Manager{
		store: appStore, container: container, log: logger,
		clients: make(map[string]*whatsmeow.Client), qrCodes: make(map[string]string), cancels: make(map[string]context.CancelFunc),
	}
	if err := m.restore(ctx, logLevel); err != nil {
		container.Close()
		return nil, err
	}
	return m, nil
}

func (m *Manager) restore(ctx context.Context, logLevel string) error {
	devices, err := m.container.GetAllDevices(ctx)
	if err != nil {
		return fmt.Errorf("memuat sesi WhatsApp: %w", err)
	}
	for _, device := range devices {
		if device.ID == nil {
			continue
		}
		number, err := m.store.FindNumberBySession(ctx, device.ID.String())
		if err != nil {
			m.log.Warn("sesi WhatsApp tidak mempunyai nomor dashboard", "jid", device.ID.String())
			continue
		}
		client := whatsmeow.NewClient(device, waLog.Stdout("WA-"+number.ID[:8], logLevel, true))
		m.register(number.ID, client)
		if err := client.Connect(); err != nil {
			m.store.UpdateNumberStatus(ctx, number.ID, "disconnected", err.Error())
			m.log.Error("gagal menghubungkan sesi", "number_id", number.ID, "error", err)
		}
	}
	return nil
}

func (m *Manager) StartPairing(ctx context.Context, numberID, logLevel string) error {
	m.mu.RLock()
	_, exists := m.clients[numberID]
	m.mu.RUnlock()
	if exists {
		return ErrPairing
	}

	device := m.container.NewDevice()
	client := whatsmeow.NewClient(device, waLog.Stdout("WA-"+numberID[:8], logLevel, true))
	client.QRClientType = whatsmeow.PairClientChrome
	pairCtx, cancel := context.WithCancel(context.Background())
	qrChan, err := client.GetQRChannel(pairCtx)
	if err != nil {
		cancel()
		return fmt.Errorf("membuat channel QR: %w", err)
	}

	m.mu.Lock()
	m.clients[numberID] = client
	m.cancels[numberID] = cancel
	m.mu.Unlock()
	m.registerHandler(numberID, client)

	go func() {
		for evt := range qrChan {
			switch evt.Event {
			case "code":
				m.mu.Lock()
				m.qrCodes[numberID] = evt.Code
				m.mu.Unlock()
				_ = m.store.UpdateNumberStatus(context.Background(), numberID, "pairing", "")
			case "success":
				m.mu.Lock()
				delete(m.qrCodes, numberID)
				m.mu.Unlock()
			case "timeout":
				_ = m.store.UpdateNumberStatus(context.Background(), numberID, "disconnected", "QR kedaluwarsa; mulai pairing kembali")
			case "error":
				_ = m.store.UpdateNumberStatus(context.Background(), numberID, "disconnected", "pairing gagal")
			}
		}
	}()
	if err := client.Connect(); err != nil {
		cancel()
		m.mu.Lock()
		delete(m.clients, numberID)
		delete(m.cancels, numberID)
		m.mu.Unlock()
		return fmt.Errorf("menghubungkan WhatsApp: %w", err)
	}
	return nil
}

func (m *Manager) register(numberID string, client *whatsmeow.Client) {
	m.mu.Lock()
	m.clients[numberID] = client
	m.mu.Unlock()
	m.registerHandler(numberID, client)
}

func (m *Manager) registerHandler(numberID string, client *whatsmeow.Client) {
	client.AddEventHandler(func(raw any) {
		switch evt := raw.(type) {
		case *events.Connected:
			if client.Store.ID != nil {
				jid := client.Store.ID.String()
				phone := normalizePhone(client.Store.ID.User)
				if err := m.store.MarkNumberConnected(context.Background(), numberID, phone, jid); err != nil {
					m.log.Error("gagal menyimpan status koneksi", "error", err)
				}
			}
		case *events.Disconnected:
			_ = m.store.UpdateNumberStatus(context.Background(), numberID, "disconnected", "koneksi terputus")
		case *events.LoggedOut:
			_ = m.store.UpdateNumberStatus(context.Background(), numberID, "logged_out", "sesi keluar dari WhatsApp")
		case *events.Message:
			if !evt.Info.IsFromMe {
				m.handleMessage(numberID, evt)
			}
		}
	})
}

func (m *Manager) handleMessage(numberID string, evt *events.Message) {
	remote := evt.Info.Sender
	if remote.IsEmpty() {
		remote = evt.Info.Chat
	}
	chat := evt.Info.Chat
	if chat.IsEmpty() {
		chat = remote
	}
	text, messageType := extractText(evt.Message)
	raw, _ := json.Marshal(evt.Message)
	msg := appstore.Message{
		NumberID: numberID, Direction: "inbound", RemotePhone: normalizePhone(remote.User),
		RemoteJID: chat.String(), WAMessageID: string(evt.Info.ID), Type: messageType,
		Text: text, Status: "received", RawJSON: raw,
	}
	id, err := m.store.CreateMessage(context.Background(), msg)
	if err != nil {
		m.log.Error("gagal menyimpan pesan masuk", "error", err, "message_id", evt.Info.ID)
		return
	}
	number, err := m.store.GetNumber(context.Background(), numberID)
	if err != nil || number.WebhookURL == "" {
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"event": "message.received", "number": map[string]any{"id": number.ID, "phone": number.Phone},
		"message": map[string]any{"id": id, "wa_message_id": evt.Info.ID, "from": msg.RemotePhone, "from_jid": remote.String(), "chat_jid": chat.String(), "type": messageType, "text": text, "timestamp": evt.Info.Timestamp},
	})
	if _, err := m.store.QueueDelivery(context.Background(), numberID, id, number.WebhookURL, payload); err != nil {
		m.log.Error("gagal membuat antrean webhook", "error", err)
	}
}

func (m *Manager) SendText(ctx context.Context, numberID, to, text string) (string, error) {
	m.mu.RLock()
	client := m.clients[numberID]
	m.mu.RUnlock()
	if client == nil || !client.IsConnected() || !client.IsLoggedIn() {
		return "", ErrNotConnected
	}
	jid := types.NewJID(normalizePhone(to), types.DefaultUserServer)
	response, err := client.SendMessage(ctx, jid, &waProto.Message{Conversation: proto.String(text)})
	if err != nil {
		return "", err
	}
	return string(response.ID), nil
}

func (m *Manager) QRCode(numberID string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	code, ok := m.qrCodes[numberID]
	return code, ok
}

func (m *Manager) RestartPairing(ctx context.Context, numberID, logLevel string) error {
	m.stop(numberID, false)
	return m.StartPairing(ctx, numberID, logLevel)
}

func (m *Manager) Logout(ctx context.Context, numberID string) error {
	m.mu.RLock()
	client := m.clients[numberID]
	m.mu.RUnlock()
	if client != nil && client.Store.ID != nil {
		if err := client.Logout(ctx); err != nil {
			return err
		}
	}
	m.stop(numberID, true)
	return nil
}

func (m *Manager) stop(numberID string, remove bool) {
	m.mu.Lock()
	client, cancel := m.clients[numberID], m.cancels[numberID]
	delete(m.clients, numberID)
	delete(m.cancels, numberID)
	delete(m.qrCodes, numberID)
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if client != nil {
		client.Disconnect()
	}
}

func (m *Manager) Close() error {
	m.mu.Lock()
	clients := make([]*whatsmeow.Client, 0, len(m.clients))
	for _, client := range m.clients {
		clients = append(clients, client)
	}
	for _, cancel := range m.cancels {
		cancel()
	}
	m.clients = make(map[string]*whatsmeow.Client)
	m.mu.Unlock()
	for _, client := range clients {
		client.Disconnect()
	}
	return m.container.Close()
}

func normalizePhone(value string) string {
	value = strings.Split(value, ":")[0]
	var out strings.Builder
	for _, char := range value {
		if char >= '0' && char <= '9' {
			out.WriteRune(char)
		}
	}
	return out.String()
}

func extractText(message *waProto.Message) (string, string) {
	if message == nil {
		return "", "unknown"
	}
	if value := message.GetConversation(); value != "" {
		return value, "text"
	}
	if value := message.GetExtendedTextMessage(); value != nil {
		return value.GetText(), "text"
	}
	if value := message.GetImageMessage(); value != nil {
		return value.GetCaption(), "image"
	}
	if value := message.GetVideoMessage(); value != nil {
		return value.GetCaption(), "video"
	}
	if value := message.GetDocumentMessage(); value != nil {
		return value.GetCaption(), "document"
	}
	if message.GetAudioMessage() != nil {
		return "", "audio"
	}
	if message.GetStickerMessage() != nil {
		return "", "sticker"
	}
	if message.GetContactMessage() != nil {
		return "", "contact"
	}
	if message.GetLocationMessage() != nil {
		return "", "location"
	}
	return "", "unknown"
}

// configureChromeIdentity sets the companion metadata used for new pairings.
// WhatsApp persists this metadata when the QR code is scanned, so existing
// linked devices need to be paired again to show the new browser label.
func configureChromeIdentity() {
	waStore.DeviceProps.PlatformType = waCompanionReg.DeviceProps_CHROME.Enum()
	waStore.SetOSInfo("Windows", [3]uint32{10, 0, 0})
	waStore.BaseClientPayload.WebInfo.Browser = proto.String("Google Chrome")
	waStore.BaseClientPayload.WebInfo.BrowserVersion = proto.String("148.0.0.0")
	waStore.BaseClientPayload.UserAgent.Device = proto.String("Desktop")
}
