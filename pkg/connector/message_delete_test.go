package connector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix/id"
	"net/http"
	"path/filepath"
	"strings"
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
			err := deletionTestClient(t, target).HandleMatrixMessageRemove(context.Background(), msg)
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

func deletionTestClient(t *testing.T, messages ...*database.Message) *GMClient {
	t.Helper()
	ctx := context.Background()
	db, err := dbutil.NewWithDialect(filepath.Join(t.TempDir(), "deletions.db")+"?_foreign_keys=on", "sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	gc := dmTestClient()
	bridgeDB := database.New("", gc.Main.GetDBMetaTypes(), db)
	if err = bridgeDB.Upgrade(ctx); err != nil {
		t.Fatal(err)
	}
	gc.Main.br = &bridgev2.Bridge{DB: bridgeDB}
	ghosts := map[networkid.UserID]bool{}
	portals := map[networkid.PortalKey]bool{}
	for i, msg := range messages {
		if !ghosts[msg.SenderID] {
			if err = bridgeDB.Ghost.Insert(ctx, &database.Ghost{ID: msg.SenderID}); err != nil {
				t.Fatal(err)
			}
			ghosts[msg.SenderID] = true
		}
		if !portals[msg.Room] {
			if err = bridgeDB.Portal.Insert(ctx, &database.Portal{PortalKey: msg.Room}); err != nil {
				t.Fatal(err)
			}
			portals[msg.Room] = true
		}
		msg.MXID = id.EventID(fmt.Sprintf("$part-%d", i))
		if err = bridgeDB.Message.Insert(ctx, msg); err != nil {
			t.Fatal(err)
		}
	}
	return gc
}

func TestMessageDeletionRefusesMultipart(t *testing.T) {
	for _, count := range []int{0, 1, 2, 3} {
		t.Run(fmt.Sprintf("parts=%d", count), func(t *testing.T) {
			portal := testMatrixText("group:88").Portal
			var parts []*database.Message
			for i := 0; i < count; i++ {
				partID := networkid.PartID("")
				if i > 0 {
					partID = networkid.PartID(fmt.Sprint(i))
				}
				parts = append(parts, &database.Message{ID: "123", PartID: partID, Room: portal.PortalKey, SenderID: "20"})
			}
			gc := deletionTestClient(t, parts...)
			calls := 0
			mockGroupMe(t, func(r *http.Request) (int, string) { calls++; return 204, "" })
			targets := parts
			if count == 0 {
				targets = []*database.Message{{ID: "123", Room: portal.PortalKey, SenderID: "20"}}
			}
			for _, target := range targets {
				err := gc.HandleMatrixMessageRemove(context.Background(), &bridgev2.MatrixMessageRemove{MatrixEventBase: bridgev2.MatrixEventBase[*event.RedactionEventContent]{Portal: portal}, TargetMessage: target})
				if count == 1 {
					if err != nil || calls != 1 {
						t.Fatalf("single part not deleted: calls=%d err=%v", calls, err)
					}
				} else {
					if calls != 0 || err == nil {
						t.Fatalf("unsafe deletion: parts=%d calls=%d err=%v", count, calls, err)
					}
					if count > 1 {
						var status bridgev2.MessageStatus
						if !errors.As(err, &status) || status.Status != event.MessageStatusFail || !status.IsCertain || !status.SendNotice || !strings.Contains(status.Message, "multiple Matrix parts") {
							t.Fatalf("missing clear permanent bridge status: %v", err)
						}
					}
				}
			}
		})
	}
}

func TestMessageDeletionDatabaseFailureDoesNotSend(t *testing.T) {
	portal := testMatrixText("group:88").Portal
	target := &database.Message{ID: "123", Room: portal.PortalKey, SenderID: "20"}
	gc := deletionTestClient(t, target)
	if err := gc.Main.br.DB.Close(); err != nil {
		t.Fatal(err)
	}
	mockGroupMe(t, func(*http.Request) (int, string) { t.Fatal("DELETE sent without verifying parts"); return 204, "" })
	err := gc.HandleMatrixMessageRemove(context.Background(), &bridgev2.MatrixMessageRemove{MatrixEventBase: bridgev2.MatrixEventBase[*event.RedactionEventContent]{Portal: portal}, TargetMessage: target})
	if err == nil {
		t.Fatal("database failure must refuse deletion")
	}
}
