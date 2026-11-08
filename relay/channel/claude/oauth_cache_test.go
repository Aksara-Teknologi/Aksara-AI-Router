package claude

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
)

func TestAddClaudeOAuthPromptCache(t *testing.T) {
	t.Setenv("PLUGIN_OAUTH_AUTO_CACHE", "")
	request := &dto.ClaudeRequest{Messages: []dto.ClaudeMessage{{Role: "user", Content: "hello"}}}
	addClaudeOAuthPromptCache(request)
	assert.JSONEq(t, `{"type":"ephemeral"}`, string(request.CacheControl))
}

func TestAddClaudeOAuthPromptCachePreservesExplicitCache(t *testing.T) {
	tests := []struct {
		name    string
		request *dto.ClaudeRequest
	}{
		{name: "top-level", request: &dto.ClaudeRequest{CacheControl: json.RawMessage(`{"type":"ephemeral","ttl":"1h"}`)}},
		{name: "system block", request: &dto.ClaudeRequest{System: []any{map[string]any{"type": "text", "text": "system", "cache_control": map[string]any{"type": "ephemeral"}}}}},
		{name: "tool", request: &dto.ClaudeRequest{Tools: []any{map[string]any{"name": "lookup", "cache_control": map[string]any{"type": "ephemeral"}}}}},
		{name: "message block", request: &dto.ClaudeRequest{Messages: []dto.ClaudeMessage{{Role: "user", Content: []any{map[string]any{"type": "text", "text": "hello", "cache_control": map[string]any{"type": "ephemeral"}}}}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := string(test.request.CacheControl)
			addClaudeOAuthPromptCache(test.request)
			assert.Equal(t, before, string(test.request.CacheControl))
		})
	}
}

func TestAddClaudeOAuthPromptCacheCanBeDisabled(t *testing.T) {
	t.Setenv("PLUGIN_OAUTH_AUTO_CACHE", "false")
	request := &dto.ClaudeRequest{Messages: []dto.ClaudeMessage{{Role: "user", Content: "hello"}}}
	addClaudeOAuthPromptCache(request)
	assert.Empty(t, request.CacheControl)
}
