package provider

import (
	"strings"
	"testing"

	"github.com/disksing/agenthub/internal/session"
)

func TestPromptTextLeavesUnmarkedUserTextUntouched(t *testing.T) {
	input := session.MessageInput{Text: "ordinary user text\nwith delimiters", Role: session.MessageRoleUser}
	got, err := PromptText(input)
	if err != nil {
		t.Fatal(err)
	}
	if got != input.Text {
		t.Fatalf("prompt text = %q, want %q", got, input.Text)
	}
}

func TestPromptTextEncodesProvenanceAsSafeJSON(t *testing.T) {
	input := session.MessageInput{
		Text:  "text with }\n\" and AGENTHUB provenance markers",
		Role:  session.MessageRoleSystem,
		Steer: true,
		Sender: &session.MessageSender{
			Name:      "Scheduler ]\n",
			SessionID: "ses_scheduler",
		},
	}
	got, err := PromptText(input)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, provenancePromptHeader) ||
		!strings.Contains(got, `"protocol":"agenthub.provenance.v1"`) ||
		!strings.Contains(got, `"role":"system"`) ||
		!strings.Contains(got, `"sessionId":"ses_scheduler"`) {
		t.Fatalf("prompt envelope is missing stable metadata: %q", got)
	}
	if strings.Contains(got, "Scheduler ]\n") || strings.Contains(got, "text with }\n\"") {
		t.Fatalf("raw sender or text escaped into the envelope: %q", got)
	}
	if !strings.Contains(got, `\n`) || !strings.Contains(got, `\"`) {
		t.Fatalf("JSON escaping is missing: %q", got)
	}
}
