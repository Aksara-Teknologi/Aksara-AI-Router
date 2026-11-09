package gemini

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

func TestAntigravityModelAliases(t *testing.T) {
	tests := map[string]string{
		"gemini-4-argon-high":   "gemini-4-argon-high(high)",
		"gemini-4-argon-medium": "gemini-4-argon-medium(medium)",
		"gemini-4-argon-low":    "gemini-4-argon-low(low)",
		"gemini-3.8-flash":      "gemini-3.8-flash-tiered",
		"gemini-3.8-flash-high": "gemini-3.8-flash-tiered(high)",
		"claude-sonnet-5-5":     "claude-sonnet-5-5-high(high)",
		"claude-opus-5-5":       "claude-opus-5-5-high(high)",
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

func TestNormalizeAntigravityRequestForAgents(t *testing.T) {
	request := map[string]any{
		"thinking":         map[string]any{"type": "enabled"},
		"safetySettings":   []any{},
		"generationConfig": map[string]any{"maxOutputTokens": float64(200000)},
		"toolConfig":       map[string]any{"functionCallingConfig": map[string]any{"mode": "AUTO"}},
		"tools": []any{
			map[string]any{"functionDeclarations": []any{map[string]any{"name": "bad name!"}}},
			map[string]any{"functionDeclarations": []any{map[string]any{"name": "bad name!"}, map[string]any{"name": "read_file", "parameters": map[string]any{"type": "object"}}}},
		},
		"contents": []any{
			map[string]any{"role": "model", "parts": []any{
				map[string]any{"thought": true, "text": "hidden"},
				map[string]any{"functionCall": map[string]any{"name": "bad name!"}},
				map[string]any{"functionCall": map[string]any{"name": "read_file"}, "thoughtSignature": "x"},
			}},
			map[string]any{"role": "model", "parts": []any{map[string]any{"functionResponse": map[string]any{"name": "bad name!"}}}},
		},
	}
	normalizeAntigravityRequest(request)

	require.NotContains(t, request, "thinking")
	require.NotContains(t, request, "safetySettings")
	assert.Equal(t, float64(antigravityMaxOutputTokens), request["generationConfig"].(map[string]any)["maxOutputTokens"])
	declarations := request["tools"].([]any)[0].(map[string]any)["functionDeclarations"].([]any)
	require.Len(t, declarations, 2)
	assert.Equal(t, "bad_name_", declarations[0].(map[string]any)["name"])
	assert.NotNil(t, declarations[0].(map[string]any)["parameters"])
	assert.Equal(t, "VALIDATED", request["toolConfig"].(map[string]any)["functionCallingConfig"].(map[string]any)["mode"])

	contents := request["contents"].([]any)
	parts := contents[0].(map[string]any)["parts"].([]any)
	require.Len(t, parts, 2)
	assert.Equal(t, antigravityDefaultSignature, parts[0].(map[string]any)["thoughtSignature"])
	assert.NotContains(t, parts[1], "thoughtSignature")
	assert.Equal(t, "user", contents[1].(map[string]any)["role"])
}

func TestAntigravityRequestIDIsStablePerSession(t *testing.T) {
	request := map[string]any{"contents": []any{1, 2, 3}}
	first := antigravityRequestID("42", "gemini-4-argon-high(high)", request)
	second := antigravityRequestID("42", "gemini-4-argon-high(high)", request)
	pattern := regexp.MustCompile(`^agent/([0-9a-f-]{36})/\d+/([0-9a-f-]{36})/5$`)
	a, b := pattern.FindStringSubmatch(first), pattern.FindStringSubmatch(second)
	require.NotNil(t, a)
	require.NotNil(t, b)
	assert.Equal(t, a[1], b[1])
	assert.Equal(t, a[2], b[2])
}
