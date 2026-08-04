package multipartutil

import "testing"

func TestValidField_RejectsCRLFAndQuote(t *testing.T) {
	cases := []struct {
		name string
		s    string
	}{
		{"CRLF", "application/pdf\r\nX-Injected: 1"},
		{"bare CR", "value\rmore"},
		{"bare LF", "value\nmore"},
		{"double quote", `value"more`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := ValidField("field name", c.s); err == nil {
				t.Fatalf("ValidField(%q) = nil, want error", c.s)
			}
		})
	}
}

func TestValidField_AllowsOrdinaryValues(t *testing.T) {
	cases := []string{"application/pdf", "report.pdf", "my_field", "", "hello world"}
	for _, s := range cases {
		if err := ValidField("field name", s); err != nil {
			t.Errorf("ValidField(%q) = %v, want nil", s, err)
		}
	}
}

func TestValidField_ErrorMentionsKind(t *testing.T) {
	err := ValidField("media type", "bad\r\nvalue")
	if err == nil {
		t.Fatal("ValidField: want error")
	}
	if got := err.Error(); got != `invalid media type: contains CR, LF, or quote` {
		t.Errorf("error = %q, want %q", got, `invalid media type: contains CR, LF, or quote`)
	}
}
