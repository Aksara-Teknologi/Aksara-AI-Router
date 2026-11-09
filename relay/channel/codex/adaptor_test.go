package codex

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetRequestURLAlphaSearch(t *testing.T) {
	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:    constant.ChannelTypeCodex,
			ChannelBaseUrl: "https://chatgpt.com",
		},
		RelayMode: relayconstant.RelayModeAlphaSearch,
	}

	url, err := adaptor.GetRequestURL(info)
	require.NoError(t, err)
	assert.Equal(t, "https://chatgpt.com/backend-api/codex/alpha/search", url)
}

func TestBufferCodexOAuthResponseStream(t *testing.T) {
	response := `{"id":"resp_1","status":"completed","model":"gpt-6-luna","output":[],"usage":{"input_tokens":2,"output_tokens":1}}`
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":" + response + "}\n\n")),
	}
	require.NoError(t, bufferCodexOAuthResponseStream(resp))
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.JSONEq(t, response, string(body))
}

func TestBufferCodexOAuthResponseStreamRequiresTerminalEvent(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n")),
	}
	assert.ErrorContains(t, bufferCodexOAuthResponseStream(resp), "without a terminal response")
}

// The Codex backend rejects these sampling fields, so the adaptor clears them
// rather than forwarding what the client sent.
func TestConvertOpenAIResponsesRequestDropsUnsupportedSamplingFields(t *testing.T) {
	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeCodex},
		RelayMode:   relayconstant.RelayModeResponses,
	}

	converted, err := adaptor.ConvertOpenAIResponsesRequest(nil, info, dto.OpenAIResponsesRequest{
		Model:            "gpt-5-codex",
		Input:            json.RawMessage(`"hello"`),
		MaxOutputTokens:  lo.ToPtr(uint(128)),
		Temperature:      lo.ToPtr(1.0),
		TopP:             lo.ToPtr(0.95),
		FrequencyPenalty: json.RawMessage(`1.5`),
		PresencePenalty:  json.RawMessage(`1.5`),
	})
	require.NoError(t, err)

	request, ok := converted.(dto.OpenAIResponsesRequest)
	require.True(t, ok)
	assert.Nil(t, request.MaxOutputTokens)
	assert.Nil(t, request.Temperature)
	assert.Nil(t, request.TopP)
	assert.Nil(t, request.FrequencyPenalty)
	assert.Nil(t, request.PresencePenalty)
}

func TestBuildCodexOAuthRequest(t *testing.T) {
	request := dto.OpenAIResponsesRequest{
		Model:              "gpt-6-astra",
		Input:              json.RawMessage(`[{"type":"message","role":"system","id":"msg_1","content":"be brief"},{"type":"item_reference","id":"rs_1"},{"type":"function_call","id":"fc_1","call_id":"c1","name":"f","arguments":"{}"}]`),
		PreviousResponseID: "resp_9",
		User:               json.RawMessage(`"u"`),
		Tools:              json.RawMessage(`[{"type":"function","function":{"name":"run","parameters":{"type":"object","properties":{"a":{"type":"string","pattern":"\\p{L}+"}}}}}]`),
		ServiceTier:        "fast",
	}
	body, err := buildCodexOAuthRequest(request, "session-1")
	require.NoError(t, err)

	assert.Equal(t, true, body["stream"])
	assert.Equal(t, false, body["store"])
	assert.Equal(t, "session-1", body["prompt_cache_key"])
	assert.Equal(t, "priority", body["service_tier"])
	assert.NotEmpty(t, body["instructions"])
	assert.NotContains(t, body, "previous_response_id")
	assert.NotContains(t, body, "user")
	assert.Equal(t, []any{"reasoning.encrypted_content"}, body["include"])
	assert.Equal(t, map[string]any{"effort": "low", "summary": "auto"}, body["reasoning"])

	input := body["input"].([]any)
	require.Len(t, input, 2)
	assert.Equal(t, "developer", input[0].(map[string]any)["role"])
	assert.NotContains(t, input[0], "id")
	assert.NotContains(t, input[1], "id")

	tool := body["tools"].([]any)[0].(map[string]any)
	assert.Equal(t, "run", tool["name"])
	assert.NotContains(t, tool, "function")
	assert.NotContains(t, tool["parameters"].(map[string]any)["properties"].(map[string]any)["a"], "pattern")
}

func TestBuildCodexOAuthRequestLiteModel(t *testing.T) {
	request := dto.OpenAIResponsesRequest{
		Model: "gpt-6.1-sol",
		Input: json.RawMessage(`"hi"`),
		Tools: json.RawMessage(`[{"type":"function","name":"run","parameters":{"type":"object"}}]`),
	}
	body, err := buildCodexOAuthRequest(request, "")
	require.NoError(t, err)

	assert.Nil(t, body["tools"])
	assert.Equal(t, "", body["instructions"])
	assert.Equal(t, false, body["parallel_tool_calls"])
	assert.Equal(t, map[string]any{"effort": "medium", "context": "all_turns"}, body["reasoning"])
	input := body["input"].([]any)
	require.Len(t, input, 3)
	assert.Equal(t, "additional_tools", input[0].(map[string]any)["type"])
	assert.Len(t, input[0].(map[string]any)["tools"], 1)
}
