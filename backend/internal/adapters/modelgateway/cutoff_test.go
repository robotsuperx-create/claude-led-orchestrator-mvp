package modelgateway

import (
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestACutOffReplyIsNamedRatherThanParsed(t *testing.T) {
	for _, reason := range []string{"length", "max_tokens"} {
		_, err := responseContent(ChatResponse{Choices: []ChatChoice{{FinishReason: reason, Message: ChatMessage{Content: `{"summary":"half a rep`}}}})
		if !errors.Is(err, ports.ErrReplyCutOff) {
			t.Fatalf("%s: err = %v, want the reply named as cut off", reason, err)
		}
	}
	content, err := responseContent(ChatResponse{Choices: []ChatChoice{{FinishReason: "stop", Message: ChatMessage{Content: "{}"}}}})
	if err != nil || content != "{}" {
		t.Fatalf("a finished reply = %q, %v", content, err)
	}
}
