package whatsapp

import (
	"testing"

	waCompanionReg "go.mau.fi/whatsmeow/proto/waCompanionReg"
	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	waStore "go.mau.fi/whatsmeow/store"
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

func TestConfigureChromeIdentity(t *testing.T) {
	configureChromeIdentity()
	if got := waStore.DeviceProps.GetPlatformType(); got != waCompanionReg.DeviceProps_CHROME {
		t.Fatalf("platform = %v", got)
	}
	if got := waStore.BaseClientPayload.GetWebInfo().GetBrowser(); got != "Google Chrome" {
		t.Fatalf("browser = %q", got)
	}
}
