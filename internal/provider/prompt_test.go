package provider

import (
	"testing"

	"github.com/disksing/agenthub/internal/session"
)

func TestPromptTextLeavesUnmarkedUserTextUntouched(t *testing.T) {
	input := session.MessageInput{
		Text: "ordinary user text\nwith delimiters", Role: session.MessageRoleUser,
		MessageID: "message-1", ReplyTo: "message-0", CorrelationID: "correlation-1",
	}
	got, err := PromptText(input)
	if err != nil {
		t.Fatal(err)
	}
	if got != input.Text {
		t.Fatalf("prompt text = %q, want %q", got, input.Text)
	}
}

func TestPromptTextLeavesOpaquePayloadTextUntouched(t *testing.T) {
	input := session.MessageInput{
		SchemaVersion: session.MessageSchemaOpaquePayload,
		Text:          "Message from application:\nkeep exactly",
		Payload:       []byte(`{"role":"anything"}`),
		Steer:         true,
	}
	got, err := PromptText(input)
	if err != nil {
		t.Fatal(err)
	}
	if got != input.Text {
		t.Fatalf("prompt text = %q, want %q", got, input.Text)
	}
}

func TestPromptTextFormatsCompactProvenance(t *testing.T) {
	tests := []struct {
		name  string
		input session.MessageInput
		want  string
	}{
		{
			name: "named user",
			input: session.MessageInput{Text: "Follow up", Role: session.MessageRoleUser,
				Sender: &session.MessageSender{Name: "disksing"}},
			want: "Message from user \"disksing\":\nFollow up",
		},
		{
			name: "steer agent",
			input: session.MessageInput{Text: "Review this", Role: session.MessageRoleAgent, Steer: true,
				Sender:    &session.MessageSender{ID: "project1.task1", Name: "project1.task2", SessionID: "ses_sender"},
				MessageID: "message-1", ReplyTo: "message-0", CorrelationID: "correlation-1"},
			want: "Message from agent \"project1.task2\" (steer):\nReview this",
		},
		{
			name:  "senderless system",
			input: session.MessageInput{Text: "Wake up", Role: session.MessageRoleSystem},
			want:  "Message from system:\nWake up",
		},
		{
			name:  "senderless user steer",
			input: session.MessageInput{Text: "Use this now", Role: session.MessageRoleUser, Steer: true},
			want:  "Message from user (steer):\nUse this now",
		},
		{
			name: "sender id fallback",
			input: session.MessageInput{Text: "From ID", Role: session.MessageRoleAgent,
				Sender: &session.MessageSender{ID: "project1.task3", SessionID: "ses_sender"}},
			want: "Message from agent \"project1.task3\":\nFrom ID",
		},
		{
			name: "sender session fallback",
			input: session.MessageInput{Text: "From session", Role: session.MessageRoleAgent,
				Sender: &session.MessageSender{SessionID: "ses_sender"}},
			want: "Message from agent \"ses_sender\":\nFrom session",
		},
		{
			name: "sender name escaping",
			input: session.MessageInput{Text: "Keep\nthe body raw", Role: session.MessageRoleSystem,
				Sender: &session.MessageSender{Name: "Scheduler\n\"spoof\""}},
			want: "Message from system \"Scheduler\\n\\\"spoof\\\"\":\nKeep\nthe body raw",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := PromptText(test.input)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("prompt text = %q, want %q", got, test.want)
			}
		})
	}
}
