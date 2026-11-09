package gemini

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/pluginoauth"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

const antigravityOAuthKey = "antigravity-oauth"

func isAntigravityOAuth(info *relaycommon.RelayInfo) bool {
	return info != nil && pluginoauth.IsManagedKey(info.ApiKey)
}

// antigravityModels maps public model ids to the tiered ids the Hub client
// sends upstream (decolua/9router, MIT). Unlisted ids pass through unchanged.
var antigravityModels = map[string]string{
	"gemini-4-argon-high":     "gemini-4-argon-high(high)",
	"gemini-4-argon-medium":   "gemini-4-argon-medium(medium)",
	"gemini-4-argon-low":      "gemini-4-argon-low(low)",
	"gemini-3.8-flash-high":   "gemini-3.8-flash-high(high)",
	"gemini-3.8-flash-medium": "gemini-3.8-flash-medium(medium)",
	"gemini-3.8-flash-low":    "gemini-3.8-flash-low(low)",
	"gemini-3.8-flash":        "gemini-3.8-flash-medium(medium)",
	"gemini-3.7-flash-high":   "gemini-3.7-flash-tiered(high)",
	"gemini-3.7-flash-medium": "gemini-3.7-flash-tiered(medium)",
	"gemini-3.7-flash-low":    "gemini-3.7-flash-tiered(low)",
	"gemini-3.6-flash-high":   "gemini-3.6-flash-tiered(high)",
	"gemini-3.6-flash-medium": "gemini-3.6-flash-tiered(medium)",
	"gemini-3.6-flash-low":    "gemini-3.6-flash-tiered(low)",
	"gemini-3.1-pro-high":     "gemini-pro-agent",
	"gemini-3.1-pro-low":      "gemini-3.1-pro-low(low)",
	"claude-sonnet-5-5-high":  "claude-sonnet-5-5-high(high)",
	"claude-sonnet-5-5":       "claude-sonnet-5-5-high(high)",
	"claude-opus-5-5-high":    "claude-opus-5-5-high(high)",
	"claude-opus-5-5":         "claude-opus-5-5-high(high)",
}

func antigravityModelName(model string) string {
	if mapped, ok := antigravityModels[model]; ok {
		return mapped
	}
	return model
}

var antigravityLevelSuffix = regexp.MustCompile(`^(.*)\(([^()]+)\)$`)

// splitAntigravityModel separates a trailing "(level)" from the model id. The
// suffix selects the thinking level and is never sent upstream as part of the
// model name; 9router strips it the same way.
func splitAntigravityModel(model string) (id string, level string) {
	match := antigravityLevelSuffix.FindStringSubmatch(strings.TrimSpace(model))
	if match == nil {
		return model, ""
	}
	return strings.TrimSpace(match[1]), strings.ToLower(strings.TrimSpace(match[2]))
}

func antigravitySessionID(c *gin.Context) string {
	sessionKey := ""
	if c != nil {
		if value, exists := c.Get(service.OAuthPoolSessionKey); exists {
			sessionKey, _ = value.(string)
		}
		if sessionKey == "" && c.Request != nil {
			sessionKey = strings.TrimSpace(c.GetHeader("X-Agent-Session-Id"))
		}
	}
	if sessionKey == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(sessionKey))
	return strconv.FormatUint(binary.BigEndian.Uint64(digest[:8]), 10)
}

func adaptAntigravityRequest(c *gin.Context, info *relaycommon.RelayInfo, request any) (any, error) {
	if !isAntigravityOAuth(info) {
		return request, nil
	}
	token, managed, err := service.ResolveNativeChannelOAuthToken(context.Background(), info.ChannelId, antigravityOAuthKey)
	if err != nil {
		return nil, err
	}
	if !managed {
		return request, nil
	}
	routing, err := service.PluginOAuthRouting(info.ChannelId, antigravityOAuthKey, token)
	if err != nil {
		return nil, err
	}
	data, err := common.Marshal(request)
	if err != nil {
		return nil, err
	}
	var geminiRequest map[string]any
	if err := common.Unmarshal(data, &geminiRequest); err != nil {
		return nil, err
	}
	sessionID := antigravitySessionID(c)
	if sessionID != "" {
		if _, exists := geminiRequest["sessionId"]; !exists {
			geminiRequest["sessionId"] = sessionID
		}
	}
	upstreamModel, level := splitAntigravityModel(antigravityModelName(info.UpstreamModelName))
	normalizeAntigravityRequest(geminiRequest)
	// Tiered ids carry no level in their name, so send it as thinking config.
	if level != "" && strings.HasSuffix(upstreamModel, "-tiered") {
		config, _ := geminiRequest["generationConfig"].(map[string]any)
		if config == nil {
			config = map[string]any{}
			geminiRequest["generationConfig"] = config
		}
		if _, exists := config["thinkingConfig"]; !exists {
			config["thinkingConfig"] = map[string]any{"thinkingLevel": level, "includeThoughts": true}
		}
	}
	return map[string]any{
		"project":   routing["projectId"],
		"model":     upstreamModel,
		"userAgent": "antigravity",
		"requestId": antigravityRequestID(sessionID, upstreamModel, geminiRequest),
		"request":   geminiRequest,
	}, nil
}

func unwrapAntigravityResponse(body []byte) []byte {
	var envelope map[string]json.RawMessage
	if common.Unmarshal(body, &envelope) != nil {
		return body
	}
	response, ok := envelope["response"]
	if !ok || len(response) == 0 || bytes.Equal(response, []byte("null")) {
		return body
	}
	return response
}

func unwrapAntigravityStream(reader io.Reader, writer io.Writer) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if bytes.HasPrefix(line, []byte("data:")) {
			payload := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
			if len(payload) != 0 && !bytes.Equal(payload, []byte("[DONE]")) {
				line = append([]byte("data: "), unwrapAntigravityResponse(payload)...)
			}
		}
		if _, err := writer.Write(append(bytes.Clone(line), '\n')); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func unwrapAntigravityHTTPResponse(resp *http.Response, stream bool) error {
	if resp == nil || resp.Body == nil || resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil
	}
	if !stream {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		if err := resp.Body.Close(); err != nil {
			return err
		}
		body = unwrapAntigravityResponse(body)
		resp.Body = io.NopCloser(bytes.NewReader(body))
		resp.ContentLength = int64(len(body))
		resp.Header.Del("Content-Length")
		return nil
	}

	reader, writer := io.Pipe()
	upstream := resp.Body
	resp.Body = reader
	resp.ContentLength = -1
	resp.Header.Del("Content-Length")
	go func() {
		defer upstream.Close()
		writer.CloseWithError(unwrapAntigravityStream(upstream, writer))
	}()
	return nil
}
