package gemini

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestAntigravityModelAliases(t *testing.T) {
	tests := map[string]string{
		"gemini-3.8-flash":    "gemini-3.8-flash-medium(medium)",
		"claude-sonnet-5-5":   "claude-sonnet-5-5-high(high)",
		"claude-opus-5-5":     "claude-opus-5-5-high(high)",
		"gemini-4-argon-high": "gemini-4-argon-high",
	}
	for input, expected := range tests {
		if actual := antigravityModelName(input); actual != expected {
			t.Errorf("antigravityModelName(%q) = %q, want %q", input, actual, expected)
		}
	}
}

func TestUnwrapAntigravityResponse(t *testing.T) {
	wrapped := []byte(`{"response":{"candidates":[{"content":{"parts":[{"text":"OK"}]}}]}}`)
	want := `{"candidates":[{"content":{"parts":[{"text":"OK"}]}}]}`
	if got := string(unwrapAntigravityResponse(wrapped)); got != want {
		t.Fatalf("unwrapped response = %s, want %s", got, want)
	}
	plain := []byte(`{"candidates":[]}`)
	if got := string(unwrapAntigravityResponse(plain)); got != string(plain) {
		t.Fatalf("plain Gemini response changed: %s", got)
	}
}

func TestUnwrapAntigravityHTTPResponse(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(`{"response":{"candidates":[]}}`)),
	}
	if err := unwrapAntigravityHTTPResponse(resp, false); err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"candidates":[]}` {
		t.Fatalf("response body = %s", body)
	}
}

func TestUnwrapAntigravityStream(t *testing.T) {
	input := "event: message\ndata: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"OK\"}]}}]}}\n\ndata: [DONE]\n\n"
	var output strings.Builder
	if err := unwrapAntigravityStream(strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	want := "event: message\ndata: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"OK\"}]}}]}\n\ndata: [DONE]\n\n"
	if output.String() != want {
		t.Fatalf("stream output = %q, want %q", output.String(), want)
	}
}

func TestUnwrapAntigravityHTTPStreamResponse(t *testing.T) {
	input := "data: {\"response\":{\"candidates\":[]}}\n\n"
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(input)),
	}
	if err := unwrapAntigravityHTTPResponse(resp, true); err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(body), "data: {\"candidates\":[]}\n\n"; got != want {
		t.Fatalf("stream response body = %q, want %q", got, want)
	}
}
