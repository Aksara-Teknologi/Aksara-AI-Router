package codex

import (
	_ "embed"
	"regexp"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// CodexCLIVersion is the Codex CLI identity sent to the ChatGPT backend.
// Behavior follows decolua/9router (MIT); bump it when the CLI is upgraded.
const CodexCLIVersion = "0.159.0"

//go:embed default_instructions.txt
var codexDefaultInstructions string

// Fields accepted by the ChatGPT Codex Responses endpoint; anything else can
// trigger "routing_unsupported" and is dropped.
var codexResponseFields = map[string]bool{
	"model": true, "input": true, "instructions": true, "tools": true, "tool_choice": true,
	"stream": true, "store": true, "reasoning": true, "service_tier": true, "include": true,
	"prompt_cache_key": true, "client_metadata": true, "text": true, "parallel_tool_calls": true,
}

// Models that carry tools and instructions as an input prefix instead of the
// top-level fields ("responses lite").
var codexLiteModels = map[string]bool{
	"gpt-6.1-sol": true, "gpt-6.1-terra": true, "gpt-6-sol": true, "gpt-6-luna": true,
}

var (
	codexServerItemID   = regexp.MustCompile(`^(rs|fc|resp|msg)_`)
	codexUnicodePattern = regexp.MustCompile(`\\p\{`)
	codexHostedTools    = map[string]bool{
		"image_generation": true, "web_search": true, "web_search_preview": true, "file_search": true,
		"computer": true, "computer_use_preview": true, "code_interpreter": true, "mcp": true,
		"local_shell": true, "tool_search": true, "namespace": true, "custom": true,
	}
)

const codexLiteContextKey = "codex_oauth_lite_transport"

func codexOAuthSessionKey(c *gin.Context) string {
	if c == nil {
		return ""
	}
	value, _ := c.Get(service.OAuthPoolSessionKey)
	sessionKey, _ := value.(string)
	return sessionKey
}

func isCodexLiteModel(model string) bool {
	return codexLiteModels[model]
}

func hasCodexWebSearch(tools []any) bool {
	for _, tool := range tools {
		if item, ok := tool.(map[string]any); ok && item["type"] == "web_search" {
			return true
		}
	}
	return false
}

// codexUsesLiteTransport reports whether the request is sent as responses-lite.
// Hosted search cannot run from the lite input prefix, so such requests use the
// regular transport.
func codexUsesLiteTransport(model string, body map[string]any) bool {
	tools, _ := body["tools"].([]any)
	return isCodexLiteModel(model) && !hasCodexWebSearch(tools)
}

func stripCodexSchemaPatterns(value any) {
	switch item := value.(type) {
	case map[string]any:
		if pattern, ok := item["pattern"].(string); ok && codexUnicodePattern.MatchString(pattern) {
			delete(item, "pattern")
		}
		for _, child := range item {
			stripCodexSchemaPatterns(child)
		}
	case []any:
		for _, child := range item {
			stripCodexSchemaPatterns(child)
		}
	}
}

func normalizeCodexInput(body map[string]any, lite bool) {
	if text, ok := body["input"].(string); ok {
		body["input"] = []any{map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": text}}}}
	}
	items, _ := body["input"].([]any)
	kept := make([]any, 0, len(items))
	for _, raw := range items {
		if text, ok := raw.(string); ok && codexServerItemID.MatchString(text) {
			continue
		}
		item, ok := raw.(map[string]any)
		if !ok {
			kept = append(kept, raw)
			continue
		}
		if item["type"] == "item_reference" {
			continue
		}
		if item["role"] == "system" && (item["type"] == nil || item["type"] == "message") {
			item["role"] = "developer"
		}
		// store=false: the backend cannot resolve server generated item ids.
		if id, ok := item["id"].(string); ok && codexServerItemID.MatchString(id) && !(lite && item["role"] == "developer" && strings.HasPrefix(id, "msg_")) {
			delete(item, "id")
		}
		kept = append(kept, item)
	}
	if len(kept) == 0 {
		kept = append(kept, map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "..."}}})
	}
	body["input"] = kept
}

func normalizeCodexTools(body map[string]any) {
	tools, ok := body["tools"].([]any)
	if !ok {
		return
	}
	names := make(map[string]bool)
	kept := make([]any, 0, len(tools))
	for _, raw := range tools {
		tool, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		toolType, _ := tool["type"].(string)
		if toolType != "function" {
			if codexHostedTools[toolType] {
				kept = append(kept, tool)
			}
			continue
		}
		function, _ := tool["function"].(map[string]any)
		name, _ := tool["name"].(string)
		if name == "" && function != nil {
			name, _ = function["name"].(string)
		}
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if len(name) > 128 {
			name = name[:128]
		}
		flat := map[string]any{"type": "function", "name": name}
		for _, key := range []string{"description", "parameters", "strict"} {
			if value, exists := tool[key]; exists {
				flat[key] = value
			} else if function != nil && function[key] != nil {
				flat[key] = function[key]
			}
		}
		if flat["parameters"] == nil {
			flat["parameters"] = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		stripCodexSchemaPatterns(flat["parameters"])
		names[name] = true
		kept = append(kept, flat)
	}
	body["tools"] = kept
	if choice, ok := body["tool_choice"].(map[string]any); ok && choice["type"] == "function" {
		if name, _ := choice["name"].(string); !names[strings.TrimSpace(name)] {
			delete(body, "tool_choice")
		}
	}
}

func normalizeCodexReasoning(body map[string]any, lite bool) {
	effort := "low"
	if lite {
		effort = "medium"
	}
	reasoning, _ := body["reasoning"].(map[string]any)
	if reasoning == nil {
		reasoning = map[string]any{}
	}
	if requested, ok := reasoning["effort"].(string); ok && requested != "" {
		effort = requested
	}
	switch {
	case lite && (effort == "none" || effort == "minimal"):
		effort = "low"
	case !lite && (effort == "max" || effort == "ultra"):
		effort = "xhigh"
	case lite && effort == "ultra":
		effort = "max"
	}
	reasoning["effort"] = effort
	if lite {
		delete(reasoning, "summary")
		reasoning["context"] = "all_turns"
	} else if _, ok := reasoning["summary"]; !ok {
		reasoning["summary"] = "auto"
	}
	body["reasoning"] = reasoning
	// The reasoning models require encrypted reasoning to continue stateless turns.
	if effort != "none" {
		body["include"] = []any{"reasoning.encrypted_content"}
	}
}

// buildCodexOAuthRequest turns a Responses request into the body the ChatGPT
// Codex backend accepts, mirroring the Codex CLI wire format.
func buildCodexOAuthRequest(request dto.OpenAIResponsesRequest, sessionKey string) (map[string]any, error) {
	data, err := common.Marshal(request)
	if err != nil {
		return nil, err
	}
	var body map[string]any
	if err := common.Unmarshal(data, &body); err != nil {
		return nil, err
	}
	lite := codexUsesLiteTransport(request.Model, body)
	normalizeCodexInput(body, lite)
	normalizeCodexTools(body)
	body["stream"] = true
	body["store"] = false
	if instructions, _ := body["instructions"].(string); strings.TrimSpace(instructions) == "" {
		body["instructions"] = ""
		if !lite {
			body["instructions"] = codexDefaultInstructions
		}
	}
	if key, _ := body["prompt_cache_key"].(string); key == "" && sessionKey != "" {
		body["prompt_cache_key"] = sessionKey
	}
	normalizeCodexReasoning(body, lite)
	if tier, _ := body["service_tier"].(string); tier == "fast" {
		body["service_tier"] = "priority"
	} else if tier != "priority" {
		delete(body, "service_tier")
	}
	if lite {
		input := body["input"].([]any)
		for _, item := range input {
			if entry, ok := item.(map[string]any); ok && entry["type"] == "additional_tools" {
				// The client already sent the lite prefix, as the Codex CLI does.
				body["instructions"] = ""
				body["tools"] = nil
				body["tool_choice"] = "auto"
				body["parallel_tool_calls"] = false
				return filterCodexFields(body), nil
			}
		}
		tools, _ := body["tools"].([]any)
		if tools == nil {
			tools = []any{}
		}
		prefix := []any{map[string]any{"type": "additional_tools", "role": "developer", "tools": tools}}
		if instructions, _ := body["instructions"].(string); strings.TrimSpace(instructions) != "" {
			prefix = append(prefix, map[string]any{"type": "message", "role": "developer", "content": []any{map[string]any{"type": "input_text", "text": instructions}}})
		} else {
			prefix = append(prefix, map[string]any{"type": "message", "role": "developer", "content": []any{map[string]any{"type": "input_text", "text": codexDefaultInstructions}}})
		}
		body["input"] = append(prefix, input...)
		body["instructions"] = ""
		body["tools"] = nil
		if body["tool_choice"] == nil {
			body["tool_choice"] = "auto"
		}
		body["parallel_tool_calls"] = false
	} else {
		delete(body, "parallel_tool_calls")
	}
	return filterCodexFields(body), nil
}

func filterCodexFields(body map[string]any) map[string]any {
	for key := range body {
		if !codexResponseFields[key] {
			delete(body, key)
		}
	}
	return body
}
