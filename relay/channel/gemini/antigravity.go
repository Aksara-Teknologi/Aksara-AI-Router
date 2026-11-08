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
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/pkg/pluginoauth"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const antigravityOAuthKey = "antigravity-oauth"

func isAntigravityOAuth(info *relaycommon.RelayInfo) bool {
	return info != nil && pluginoauth.IsManagedKey(info.ApiKey)
}

func antigravityModelName(model string) string {
	aliases := map[string]string{
		"gemini-3.8-flash":  "gemini-3.8-flash-medium(medium)",
		"claude-sonnet-5-5": "claude-sonnet-5-5-high(high)",
		"claude-opus-5-5":   "claude-opus-5-5-high(high)",
	}
	if mapped, ok := aliases[model]; ok {
		return mapped
	}
	return model
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
	data, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	var geminiRequest map[string]any
	if err := json.Unmarshal(data, &geminiRequest); err != nil {
		return nil, err
	}
	if sessionID := antigravitySessionID(c); sessionID != "" {
		if _, exists := geminiRequest["sessionId"]; !exists {
			geminiRequest["sessionId"] = sessionID
		}
	}
	taskID := uuid.NewString()
	return map[string]any{
		"project":   routing["projectId"],
		"model":     antigravityModelName(info.UpstreamModelName),
		"userAgent": "antigravity",
		"requestId": "agent/" + taskID + "/" + strconv.FormatInt(time.Now().Unix(), 10) + "/" + taskID + "/1",
		"request":   geminiRequest,
	}, nil
}

func unwrapAntigravityResponse(body []byte) []byte {
	var envelope map[string]json.RawMessage
	if json.Unmarshal(body, &envelope) != nil {
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
