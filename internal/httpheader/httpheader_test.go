package httpheader

import (
	"net/http"
	"testing"
)

func TestApply_SetsHeaders(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://example.com", nil)
	Apply(req, map[string]string{"X-Foo": "bar", "X-Baz": "qux"}, "Authorization")

	if got := req.Header.Get("X-Foo"); got != "bar" {
		t.Errorf("X-Foo = %q, want %q", got, "bar")
	}
	if got := req.Header.Get("X-Baz"); got != "qux" {
		t.Errorf("X-Baz = %q, want %q", got, "qux")
	}
}

func TestApply_SkipsAuthHeaderCaseInsensitively(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://example.com", nil)
	req.Header.Set("Authorization", "original")
	Apply(req, map[string]string{"authorization": "attacker-supplied", "X-Foo": "bar"}, "Authorization")

	if got := req.Header.Get("Authorization"); got != "original" {
		t.Errorf("Authorization = %q, want %q (auth header must not be overridden)", got, "original")
	}
	if got := req.Header.Get("X-Foo"); got != "bar" {
		t.Errorf("X-Foo = %q, want %q", got, "bar")
	}
}

func TestApply_NilMapNoOp(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://example.com", nil)
	req.Header.Set("Authorization", "original")
	Apply(req, nil, "Authorization")

	if got := len(req.Header); got != 1 {
		t.Errorf("len(req.Header) = %d, want 1 (only Authorization)", got)
	}
	if got := req.Header.Get("Authorization"); got != "original" {
		t.Errorf("Authorization = %q, want %q", got, "original")
	}
}

func TestApply_EmptyAuthHeaderNameAppliesEverything(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://example.com", nil)
	Apply(req, map[string]string{"Authorization": "should-be-set", "X-Foo": "bar"}, "")

	if got := req.Header.Get("Authorization"); got != "should-be-set" {
		t.Errorf("Authorization = %q, want %q (empty authHeaderName means no skip)", got, "should-be-set")
	}
	if got := req.Header.Get("X-Foo"); got != "bar" {
		t.Errorf("X-Foo = %q, want %q", got, "bar")
	}
}
