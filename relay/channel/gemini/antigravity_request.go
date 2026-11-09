package gemini

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
)

const (
	antigravityMaxOutputTokens = 64000
	// Documented Gemini 3 bypass for function calls replayed without a signature.
	antigravityDefaultSignature = "skip_thought_signature_validator"
)

// Fields generateContent rejects when set by OpenAI/Claude style thinking options.
var antigravityRejectedFields = []string{
	"output_config", "thinking", "reasoning_effort", "reasoning", "enable_thinking",
	"thinking_budget", "thinkingConfig", "safetySettings",
}

var antigravityInvalidNameChars = regexp.MustCompile(`[^a-zA-Z0-9_.:\-]`)

func antigravityFunctionName(name string) string {
	if name == "" {
		return "_unknown"
	}
	name = antigravityInvalidNameChars.ReplaceAllString(name, "_")
	if c := name[0]; !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_') {
		name = "_" + name
	}
	if len(name) > 64 {
		name = name[:64]
	}
	return name
}

// antigravityUUID derives a stable UUID so one conversation keeps one id.
func antigravityUUID(seed string) string {
	digest := sha256.Sum256([]byte(seed))
	var id uuid.UUID
	copy(id[:], digest[:16])
	id[6] = id[6]&0x0f | 0x50
	id[8] = id[8]&0x3f | 0x80
	return id.String()
}

// antigravityRequestID has the agent/<conversation>/<ms>/<trajectory>/<step>
// shape. The conversation and trajectory ids stay stable within a session.
func antigravityRequestID(sessionID string, model string, request map[string]any) string {
	if sessionID == "" {
		sessionID = uuid.NewString()
	}
	contents, _ := request["contents"].([]any)
	step := max(1, len(contents)*2-1)
	return fmt.Sprintf("agent/%s/%d/%s/%d",
		antigravityUUID("antigravity:conversation:"+sessionID),
		time.Now().UnixMilli(),
		antigravityUUID("antigravity:trajectory:"+sessionID+":"+model+":agent"),
		step)
}

// normalizeAntigravityRequest adapts a generateContent body to what the Code
// Assist endpoint accepts for tool-calling agents.
func normalizeAntigravityRequest(request map[string]any) {
	for _, key := range antigravityRejectedFields {
		delete(request, key)
	}
	if config, ok := request["generationConfig"].(map[string]any); ok {
		if limit, ok := config["maxOutputTokens"].(float64); ok && limit > antigravityMaxOutputTokens {
			config["maxOutputTokens"] = float64(antigravityMaxOutputTokens)
		}
	}
	if contents, ok := request["contents"].([]any); ok {
		for _, rawContent := range contents {
			content, ok := rawContent.(map[string]any)
			if !ok {
				continue
			}
			parts, _ := content["parts"].([]any)
			kept := make([]any, 0, len(parts))
			signedCall := false
			for _, rawPart := range parts {
				part, ok := rawPart.(map[string]any)
				if !ok {
					kept = append(kept, rawPart)
					continue
				}
				_, isCall := part["functionCall"]
				_, isResponse := part["functionResponse"]
				_, hasText := part["text"]
				if part["thought"] == true && !isCall {
					continue
				}
				if _, signed := part["thoughtSignature"]; signed && !isCall && !hasText {
					continue
				}
				if isResponse {
					content["role"] = "user"
					if response, ok := part["functionResponse"].(map[string]any); ok {
						name, _ := response["name"].(string)
						response["name"] = antigravityFunctionName(name)
					}
				}
				if call, ok := part["functionCall"].(map[string]any); ok {
					name, _ := call["name"].(string)
					call["name"] = antigravityFunctionName(name)
					// Gemini 3 needs a signature on the first call of a turn; siblings stay unsigned.
					if !signedCall {
						if _, has := part["thoughtSignature"]; !has {
							part["thoughtSignature"] = antigravityDefaultSignature
						}
						signedCall = true
					} else {
						delete(part, "thoughtSignature")
					}
				}
				kept = append(kept, part)
			}
			content["parts"] = kept
		}
	}
	groups, _ := request["tools"].([]any)
	delete(request, "tools")
	delete(request, "toolConfig")
	seen := make(map[string]bool)
	declarations := make([]any, 0)
	for _, rawGroup := range groups {
		group, _ := rawGroup.(map[string]any)
		functions, _ := group["functionDeclarations"].([]any)
		for _, rawFunction := range functions {
			function, ok := rawFunction.(map[string]any)
			if !ok {
				continue
			}
			name, _ := function["name"].(string)
			name = antigravityFunctionName(name)
			if seen[name] {
				continue
			}
			seen[name] = true
			function["name"] = name
			if function["parameters"] == nil && function["parametersJsonSchema"] == nil {
				function["parameters"] = map[string]any{
					"type":       "object",
					"properties": map[string]any{"reason": map[string]any{"type": "string", "description": "Brief explanation"}},
					"required":   []any{"reason"},
				}
			}
			declarations = append(declarations, function)
		}
	}
	if len(declarations) > 0 {
		request["tools"] = []any{map[string]any{"functionDeclarations": declarations}}
		request["toolConfig"] = map[string]any{"functionCallingConfig": map[string]any{"mode": "VALIDATED"}}
	}
}
