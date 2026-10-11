package httpserver

import "testing"

func TestNormalizePhone(t *testing.T) {
	if got := normalizePhone("+62 812-3456-789"); got != "628123456789" {
		t.Fatalf("normalizePhone() = %q", got)
	}
}

func TestNormalizeRecipientConvertsIndonesianLocalNumber(t *testing.T) {
	if got := normalizeRecipient("0897-1169-692"); got != "628971169692" {
		t.Fatalf("normalizeRecipient() = %q", got)
	}
}

func TestValidHTTPURL(t *testing.T) {
	cases := []struct {
		value string
		valid bool
	}{
		{"https://example.com/hooks/wa", true},
		{"http://127.0.0.1:9000/webhook", true},
		{"javascript:alert(1)", false},
		{"example.com/webhook", false},
	}
	for _, tc := range cases {
		if got := validHTTPURL(tc.value); got != tc.valid {
			t.Errorf("validHTTPURL(%q) = %v", tc.value, got)
		}
	}
}
