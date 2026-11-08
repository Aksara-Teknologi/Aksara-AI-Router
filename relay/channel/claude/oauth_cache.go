package claude

import (
	"encoding/json"
	"os"

	"github.com/QuantumNous/new-api/relaykit/dto"
)

func hasClaudeCacheControl(value any) bool {
	encoded, err := json.Marshal(value)
	if err != nil {
		return false
	}
	var decoded any
	if json.Unmarshal(encoded, &decoded) != nil {
		return false
	}
	var visit func(any) bool
	visit = func(current any) bool {
		switch item := current.(type) {
		case map[string]any:
			if _, exists := item["cache_control"]; exists {
				return true
			}
			for _, child := range item {
				if visit(child) {
					return true
				}
			}
		case []any:
			for _, child := range item {
				if visit(child) {
					return true
				}
			}
		}
		return false
	}
	return visit(decoded)
}

func addClaudeOAuthPromptCache(request *dto.ClaudeRequest) {
	if request == nil || os.Getenv("PLUGIN_OAUTH_AUTO_CACHE") == "false" || len(request.CacheControl) > 0 {
		return
	}
	if hasClaudeCacheControl(request.System) || hasClaudeCacheControl(request.Tools) || hasClaudeCacheControl(request.Messages) {
		return
	}
	request.CacheControl = json.RawMessage(`{"type":"ephemeral"}`)
}
