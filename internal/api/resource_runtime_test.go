package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/disksing/agenthub/internal/session"
)

func TestCreateSessionIdempotencyAndSourceMetadataAPI(t *testing.T) {
	store, err := session.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(store, "test", time.Now()).Handler())
	defer server.Close()
	body := map[string]any{
		"title": "generation", "cwd": t.TempDir(), "agentName": "Agent",
		"idempotencyKey": "generation-stable-1",
		"source": map[string]any{
			"app": "forge", "instanceId": "workspace-1", "externalId": "project1.task2/1",
			"metadata": map[string]string{"resourceId": "project1.task2", "generationId": "generation-stable-1"},
		},
	}
	post := func(value any) (int, session.Session, bool) {
		t.Helper()
		encoded, _ := json.Marshal(value)
		response, err := http.Post(server.URL+"/v1/sessions", "application/json", bytes.NewReader(encoded))
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var result struct {
			Session session.Session `json:"session"`
			Created bool            `json:"created"`
		}
		_ = json.NewDecoder(response.Body).Decode(&result)
		return response.StatusCode, result.Session, result.Created
	}
	status, first, created := post(body)
	if status != http.StatusCreated || !created || first.Source.Metadata["resourceId"] != "project1.task2" {
		t.Fatalf("first = %d %+v %v", status, first, created)
	}
	status, second, created := post(body)
	if status != http.StatusOK || created || second.ID != first.ID {
		t.Fatalf("retry = %d %+v %v", status, second, created)
	}
	body["title"] = "conflict"
	status, _, _ = post(body)
	if status != http.StatusConflict {
		t.Fatalf("conflict status = %d", status)
	}
}

func TestTurnsAPIReadsArchivedSession(t *testing.T) {
	store, err := session.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.Create(session.CreateInput{Cwd: t.TempDir(), AgentName: "Agent"})
	if err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(session.MessageInput{Text: "hello", Role: session.MessageRoleUser})
	reply, _ := json.Marshal(map[string]string{"text": "world"})
	state, _ := json.Marshal(session.StateEventData{State: session.StateStopped})
	for _, event := range []struct {
		typeName string
		turnID   string
		data     []byte
	}{{session.EventMessageInput, "turn_1", input}, {"turn.started", "turn_1", nil}, {"message.assistant.delta", "turn_1", reply}, {session.EventTurnCompleted, "turn_1", nil}, {"session.state", "", state}} {
		if _, err := store.Append(created.ID, event.typeName, event.turnID, event.data); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.Archive(created.ID); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(store, "test", time.Now()).Handler())
	defer server.Close()
	response, err := http.Get(server.URL + "/v1/sessions/" + created.ID + "/turns?latest=true&limit=1")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result struct {
		Turns []session.TurnSummary `json:"turns"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || len(result.Turns) != 1 || result.Turns[0].FinalReplyPreview != "world" {
		t.Fatalf("turn response = %s %+v", response.Status, result.Turns)
	}
}
