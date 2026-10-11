package whatsapp

import (
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
