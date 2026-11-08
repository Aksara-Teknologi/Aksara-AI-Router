package codex

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

func codexOAuthTerminalResponse(body []byte) json.RawMessage {
	var event struct {
		Type     string          `json:"type"`
		Response json.RawMessage `json:"response"`
	}
	if json.Unmarshal(body, &event) != nil {
		return nil
	}
	switch event.Type {
	case "response.completed", "response.done", "response.incomplete", "response.failed":
		return event.Response
	default:
		return nil
	}
}

func bufferCodexOAuthResponseStream(resp *http.Response) error {
	if resp == nil || resp.Body == nil {
		return errors.New("Codex returned an empty response")
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	var terminal json.RawMessage
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		if response := codexOAuthTerminalResponse([]byte(data)); len(response) > 0 && !bytes.Equal(response, []byte("null")) {
			terminal = bytes.Clone(response)
		}
	}
	readErr := scanner.Err()
	closeErr := resp.Body.Close()
	if readErr != nil {
		return readErr
	}
	if closeErr != nil {
		return closeErr
	}
	if len(terminal) == 0 {
		return errors.New("Codex stream ended without a terminal response")
	}
	resp.Body = io.NopCloser(bytes.NewReader(terminal))
	resp.ContentLength = int64(len(terminal))
	resp.Header.Del("Content-Length")
	return nil
}
