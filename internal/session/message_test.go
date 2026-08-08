package session

import (
	"errors"
	"testing"
)

func TestNormalizeMessageInputDefaultsToUserAndPreservesText(t *testing.T) {
	value, err := NormalizeMessageInput(MessageInput{Text: "  keep surrounding whitespace  "})
	if err != nil {
		t.Fatal(err)
	}
	if value.Role != MessageRoleUser || value.Text != "  keep surrounding whitespace  " || value.Steer {
		t.Fatalf("normalized input = %+v", value)
	}
}

func TestNormalizeMessageInputKeepsProvenanceAndFutureReferences(t *testing.T) {
	value, err := NormalizeMessageInput(MessageInput{
		Text:          "wake up",
		Role:          MessageRoleAgent,
		Sender:        &MessageSender{Name: " Worker ", SessionID: " ses_source "},
		Steer:         true,
		MessageID:     "message-1",
		ReplyTo:       "message-0",
		CorrelationID: "corr-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if value.Sender == nil || value.Sender.Name != "Worker" || value.Sender.SessionID != "ses_source" {
		t.Fatalf("normalized sender = %+v", value.Sender)
	}
	if value.MessageID != "message-1" || value.ReplyTo != "message-0" || value.CorrelationID != "corr-1" {
		t.Fatalf("normalized references = %+v", value)
	}
}

func TestNormalizeMessageInputRejectsUnsupportedRolesAndSenders(t *testing.T) {
	tests := []struct {
		name string
		in   MessageInput
		code string
	}{
		{name: "assistant", in: MessageInput{Text: "spoof", Role: MessageRoleAssistant}, code: "assistant_message_forbidden"},
		{name: "unknown role", in: MessageInput{Text: "spoof", Role: "developer"}, code: "invalid_message_role"},
		{name: "empty sender", in: MessageInput{Text: "notice", Role: MessageRoleSystem, Sender: &MessageSender{}}, code: "invalid_message_sender"},
		{name: "blank text", in: MessageInput{Role: MessageRoleUser}, code: "invalid_message_text"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NormalizeMessageInput(test.in)
			var inputErr *MessageInputError
			if !errors.As(err, &inputErr) || inputErr.Code != test.code {
				t.Fatalf("error = %v, want code %q", err, test.code)
			}
		})
	}
}
