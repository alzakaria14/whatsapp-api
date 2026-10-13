package whatsapp

import (
	"database/sql"
	"path/filepath"
	"testing"

	waCompanionReg "go.mau.fi/whatsmeow/proto/waCompanionReg"
	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	waStore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

func TestExtractText(t *testing.T) {
	text, kind := extractText(&waProto.Message{Conversation: proto.String("halo")})
	if text != "halo" || kind != "text" {
		t.Fatalf("extractText() = %q, %q", text, kind)
	}
}

func TestNormalizePhone(t *testing.T) {
	if got := normalizePhone("628123456789:17"); got != "628123456789" {
		t.Fatalf("normalizePhone() = %q", got)
	}
}

func TestPhoneNumberJIDPrefersPhoneNumberAlternate(t *testing.T) {
	primary := types.NewJID("123456789012345", types.HiddenUserServer)
	alternate := types.NewJID("628123456789", types.DefaultUserServer)
	if got := phoneNumberJID(primary, alternate); got != alternate {
		t.Fatalf("phoneNumberJID() = %s, want %s", got, alternate)
	}
}

func TestPhoneNumberJIDKeepsPhoneNumberPrimary(t *testing.T) {
	primary := types.NewJID("628123456789", types.DefaultUserServer)
	alternate := types.NewJID("123456789012345", types.HiddenUserServer)
	if got := phoneNumberJID(primary, alternate); got != primary {
		t.Fatalf("phoneNumberJID() = %s, want %s", got, primary)
	}
}

func TestConfigureChromeIdentity(t *testing.T) {
	configureChromeIdentity()
	if got := waStore.DeviceProps.GetPlatformType(); got != waCompanionReg.DeviceProps_CHROME {
		t.Fatalf("platform = %v", got)
	}
	if got := waStore.BaseClientPayload.GetWebInfo().GetBrowser(); got != "Google Chrome" {
		t.Fatalf("browser = %q", got)
	}
}

func TestConfigureSQLiteSessionDSNAddsLockSettings(t *testing.T) {
	got := configureSQLiteSessionDSN("file:data/whatsapp.db?_foreign_keys=on")
	want := "file:data/whatsapp.db?_busy_timeout=10000&_foreign_keys=on&_journal_mode=WAL"
	if got != want {
		t.Fatalf("configureSQLiteSessionDSN() = %q, want %q", got, want)
	}
}

func TestConfigureSQLiteSessionDSNRespectsExplicitSettings(t *testing.T) {
	got := configureSQLiteSessionDSN("file:data/whatsapp.db?_busy_timeout=25000&_journal=DELETE")
	want := "file:data/whatsapp.db?_busy_timeout=25000&_journal=DELETE"
	if got != want {
		t.Fatalf("configureSQLiteSessionDSN() = %q, want %q", got, want)
	}
}

func TestConfigureSQLiteSessionDSNConfiguresDriverPragmas(t *testing.T) {
	dsn := configureSQLiteSessionDSN("file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "session.db")) + "?_foreign_keys=on")
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var busyTimeout int
	if err := db.QueryRow("PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		t.Fatal(err)
	}
	if busyTimeout != 10000 {
		t.Fatalf("busy_timeout = %d, want 10000", busyTimeout)
	}
	var journalMode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatal(err)
	}
	if journalMode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", journalMode)
	}
}
