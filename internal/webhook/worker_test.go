package webhook

import "testing"

func TestSignature(t *testing.T) {
	got := signature([]byte("secret"), []byte(`{"event":"message.received"}`))
	want := "94fd5123e247eea4fe3a429e01206578a3aee1f7719efa1e495ac7a98c79445d"
	if got != want {
		t.Fatalf("signature() = %q, want %q", got, want)
	}
}
