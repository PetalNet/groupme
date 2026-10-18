package connector

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/beeper/groupme-lib"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
)

func TestMessageDeletionOutgoingScope(t *testing.T) {
	for _, tc := range []struct {
		name             string
		portal           networkid.PortalID
		sender           networkid.UserID
		otherRoom        bool
		message          networkid.MessageID
		wantConversation string
	}{
		{"topic", "group:88", "20", false, "123", "88"},
		{"DM", "dm:9", "20", false, "123", "9+20"},
		{"other author", "group:88", "9", false, "123", ""},
		{"other room", "group:88", "20", true, "123", ""},
		{"invalid target", "group:88", "20", false, "", ""},
		{"poll vote", "group:88", "20", false, "poll-vote:123", ""},
		{"space", "space:7", "20", false, "123", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			mockGroupMe(t, func(r *http.Request) (int, string) {
				called = true
				if r.Method != http.MethodDelete || r.URL.EscapedPath() != "/v3/conversations/"+tc.wantConversation+"/messages/123" {
					t.Fatalf("wrong native deletion: %s %s", r.Method, r.URL.EscapedPath())
				}
				return 204, ""
			})
			portal := testMatrixText(tc.portal).Portal
			target := &database.Message{ID: tc.message, SenderID: tc.sender, Room: portal.PortalKey}
			if tc.otherRoom {
				target.Room.ID = "group:7"
			}
			msg := &bridgev2.MatrixMessageRemove{MatrixEventBase: bridgev2.MatrixEventBase[*event.RedactionEventContent]{Portal: portal}, TargetMessage: target}
			err := dmTestClient().HandleMatrixMessageRemove(context.Background(), msg)
			if called != (tc.wantConversation != "") || (err == nil) != (tc.wantConversation != "") {
				t.Fatalf("called=%v error=%v", called, err)
			}
		})
	}
}

func TestMessageDeletionPushShapes(t *testing.T) {
	gc := dmTestClient()
	for _, actor := range []string{"sender", "admin", "system"} {
		raw := activityPush{"/group/88", map[string]interface{}{"type": "message.deleted", "subject": map[string]string{"id": "123", "group_id": "88", "deleted_at": "2026-10-17T12:30:00Z", "deletion_actor": actor}}}
		evt, handled := gc.parseMessageDeletion(raw)
		if !handled || evt == nil || evt.TargetMessage != "123" || evt.PortalKey.ID != "group:88" || evt.Sender != (bridgev2.EventSender{}) || evt.CreatePortal || evt.OnlyForMe {
			t.Fatalf("incorrect raw deletion: %+v", evt)
		}
		data, _ := json.Marshal(map[string]interface{}{"message_id": "123", "deleted_at": evt.Timestamp.Unix(), "deletion_actor": actor})
		msg := &groupme.Message{ID: "999", GroupID: "88", UserID: "system", System: true, Event: &groupme.Event{Type: "message.deleted", Data: data}}
		system, handled := gc.systemMessageDeletion(msg)
		if !handled || system == nil || system.TargetMessage != evt.TargetMessage || system.PortalKey != evt.PortalKey || !system.Timestamp.Equal(evt.Timestamp) || system.Sender != (bridgev2.EventSender{}) {
			t.Fatalf("system deletion differs from raw deletion: %+v", system)
		}
	}
}

func TestMalformedMessageDeletionConsumed(t *testing.T) {
	gc := dmTestClient()
	for _, subject := range []map[string]interface{}{
		{},
		{"id": "123", "group_id": "88", "deleted_at": "invalid", "deletion_actor": "sender"},
		{"id": "123", "group_id": "99", "deleted_at": "2026-10-17T12:30:00Z", "deletion_actor": "sender"},
		{"id": "123", "group_id": "88", "deleted_at": "2026-10-17T12:30:00Z", "deletion_actor": "20"},
		{"id": "", "group_id": "88", "deleted_at": "2026-10-17T12:30:00Z", "deletion_actor": "sender"},
	} {
		evt, handled := gc.parseMessageDeletion(activityPush{"/group/88", map[string]interface{}{"type": "message.deleted", "subject": subject}})
		if !handled || evt != nil {
			t.Fatalf("accepted malformed deletion: %+v", evt)
		}
	}
	for _, data := range []string{`{}`, `{"message_id":"123","deleted_at":0,"deletion_actor":"sender"}`, `{"message_id":"123","deleted_at":"2026-10-17T12:30:00Z","deletion_actor":"sender"}`, `{"message_id":"","deleted_at":1792240200,"deletion_actor":"sender"}`} {
		evt, handled := gc.systemMessageDeletion(&groupme.Message{ID: "999", GroupID: "88", UserID: "system", System: true, Event: &groupme.Event{Type: "message.deleted", Data: json.RawMessage(data)}})
		if !handled || evt != nil {
			t.Fatalf("accepted malformed system deletion: %+v", evt)
		}
	}
	if _, handled := gc.parseMessageDeletion(activityPush{"/group/88", map[string]interface{}{"type": "line.create"}}); handled {
		t.Fatal("consumed unrelated event")
	}
	if _, handled := gc.systemMessageDeletion(&groupme.Message{}); handled {
		t.Fatal("consumed ordinary message")
	}
	for _, msg := range []*groupme.Message{
		{GroupID: "88", UserID: "20", System: true},
		{GroupID: "88", UserID: "system", System: false},
		{GroupID: "", UserID: "system", System: true},
	} {
		msg.Event = &groupme.Event{Type: "message.deleted", Data: json.RawMessage(`{"message_id":"123","deleted_at":1792240200,"deletion_actor":"sender"}`)}
		if evt, handled := gc.systemMessageDeletion(msg); !handled || evt != nil {
			t.Fatal("accepted deletion without system sender and group identity")
		}
	}
}
