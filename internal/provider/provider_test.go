package provider

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/disksing/agenthub/internal/config"
)

type writeCloser struct{ io.Writer }

func (writeCloser) Close() error { return nil }

func TestCodexTranslatesStreamingTurnAndApproval(t *testing.T) {
	var events []Event
	var approvalID string
	options := Options{
		Agent: config.Agent{Options: map[string]string{"approval": "on-request"}},
		Hooks: Hooks{
			Event:    func(event Event) { events = append(events, event) },
			Approval: func(id, _ string, _ json.RawMessage) { approvalID = id },
		},
	}
	value := newCodex("unused", options)
	var output bytes.Buffer
	value.rpc.stdin = writeCloser{&output}
	value.notification("turn/started", json.RawMessage(`{"turn":{"id":"turn-native"}}`))
	value.notification("item/agentMessage/delta", json.RawMessage(`{"delta":"hello"}`))
	value.inbound(json.RawMessage(`42`), "item/commandExecution/requestApproval", json.RawMessage(`{"command":"go test ./..."}`))
	if approvalID != "42" {
		t.Fatalf("approval id = %q", approvalID)
	}
	if err := value.Approve("42", "accept"); err != nil {
		t.Fatal(err)
	}
	value.notification("turn/completed", json.RawMessage(`{"turn":{"id":"turn-native"}}`))
	if len(events) != 3 || events[1].Type != "message.assistant.delta" || !events[2].TurnDone {
		t.Fatalf("unexpected events: %+v", events)
	}
	if !strings.Contains(output.String(), `"decision":"accept"`) {
		t.Fatalf("unexpected approval response: %s", output.String())
	}
}

func TestACPTranslatesMessageAndPermission(t *testing.T) {
	var events []Event
	var approvalID string
	value := newACP("unused", Options{
		Provider: config.Provider{Type: "kimi"},
		Hooks: Hooks{
			Event:    func(event Event) { events = append(events, event) },
			Approval: func(id, _ string, _ json.RawMessage) { approvalID = id },
		},
	})
	var output bytes.Buffer
	value.rpc.stdin = writeCloser{&output}
	value.notification("session/update", json.RawMessage(`{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"hi"}}}`))
	value.inbound(json.RawMessage(`"req-1"`), "session/request_permission", json.RawMessage(`{"options":[{"optionId":"once","kind":"allow_once"}]}`))
	if approvalID != "req-1" {
		t.Fatalf("approval id = %q", approvalID)
	}
	if err := value.Approve("req-1", "accept"); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Type != "message.assistant.delta" {
		t.Fatalf("unexpected events: %+v", events)
	}
	if !strings.Contains(output.String(), `"optionId":"once"`) {
		t.Fatalf("unexpected permission response: %s", output.String())
	}
}

func TestPiTranslatesDeltaAndSettled(t *testing.T) {
	var events []Event
	value := newPi("unused", Options{Hooks: Hooks{Event: func(event Event) { events = append(events, event) }}})
	value.event("message_update", json.RawMessage(`{"assistantMessageEvent":{"type":"text_delta","delta":"ok"}}`))
	value.event("agent_settled", json.RawMessage(`{"type":"agent_settled"}`))
	if len(events) != 2 || events[0].Type != "message.assistant.delta" || !events[1].TurnDone {
		t.Fatalf("unexpected events: %+v", events)
	}
}
