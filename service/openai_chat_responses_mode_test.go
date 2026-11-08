package service

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
)

func TestCodexChatCompletionsUseResponsesByDefault(t *testing.T) {
	if !ShouldChatCompletionsUseResponsesGlobal(1, constant.ChannelTypeCodex, "gpt-6-luna") {
		t.Fatal("Codex chat-completions traffic must use the Responses compatibility route")
	}
}
