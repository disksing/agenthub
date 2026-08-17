package session

import (
	"bytes"
	"errors"
	"testing"
)

func TestNormalizeOpaqueMessageKeepsTextAndPayload(t *testing.T) {
	payload := []byte(`{"schema":"app.v1","role":"custom"}`)
	value, err := NormalizeMessageInput(MessageInput{
		SchemaVersion: MessageSchemaOpaquePayload,
		Text:          "  keep provider text  ",
		Payload:       payload,
		MessageID:     " message-1 ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if value.Text != "  keep provider text  " || value.MessageID != "message-1" || !bytes.Equal(value.Payload, payload) {
		t.Fatalf("normalized input = %+v", value)
	}
	payload[1] = 'X'
	if bytes.Equal(value.Payload, payload) {
		t.Fatal("normalized payload aliases caller storage")
	}
}

func TestNormalizeOpaqueMessageCompactsPayloadForStableIdempotency(t *testing.T) {
	value, err := NormalizeMessageInput(MessageInput{
		SchemaVersion: MessageSchemaOpaquePayload,
		Text:          "provider text",
		Payload:       []byte(`{ "schema": "app.v1", "value": [1, 2] }`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(value.Payload), `{"schema":"app.v1","value":[1,2]}`; got != want {
		t.Fatalf("payload = %q, want %q", got, want)
	}
}

func TestNormalizeOpaqueMessageRejectsSchemaMixing(t *testing.T) {
	tests := []struct {
		name string
		in   MessageInput
		code string
	}{
		{name: "legacy role", in: MessageInput{SchemaVersion: 2, Text: "x", Role: MessageRoleAgent}, code: "mixed_message_schema"},
		{name: "legacy correlation", in: MessageInput{SchemaVersion: 2, Text: "x", ReplyTo: "old"}, code: "mixed_message_schema"},
		{name: "payload without version", in: MessageInput{Text: "x", Payload: []byte(`{}`)}, code: "mixed_message_schema"},
		{name: "unknown version", in: MessageInput{SchemaVersion: 3, Text: "x"}, code: "invalid_message_schema"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NormalizeMessageInput(test.in)
			var inputErr *MessageInputError
			if !errors.As(err, &inputErr) || inputErr.Code != test.code {
				t.Fatalf("error = %v, want %q", err, test.code)
			}
		})
	}
}

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
