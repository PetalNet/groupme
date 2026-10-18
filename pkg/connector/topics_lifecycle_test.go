package connector

import (
	"context"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/bridgeconfig"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"net/http"
	"testing"
)

func TestLiveFirstTopicMetadata(t *testing.T) {
	gc, chat := discoverTestTopic(t, nil)
	push := activityPush{channel: "/group/88", data: map[string]any{"type": "line.create", "subject": map[string]any{"group_id": "88", "parent_id": "7"}}}
	resync := gc.topicPushResync(push)
	if resync == nil || resync.PortalKey != chat.portalKey {
		t.Fatal("lost live-first topic parent")
	}
	info, err := resync.GetChatInfo(context.Background(), testMatrixText("group:88").Portal)
	if err != nil || info.ParentID == nil || *info.ParentID != "space:7" {
		t.Fatalf("live topic metadata: %+v %v", info, err)
	}
}
func TestTopicParentPreservedWhenMetadataFails(t *testing.T) {
	mockGroupMe(t, func(*http.Request) (int, string) { return 503, `{}` })
	gc := dmTestClient()
	evt := gc.topicPushResync(activityPush{channel: "/group/88", data: map[string]any{"type": "line.create", "subject": map[string]any{"group_id": "88", "parent_id": "7"}}})
	info, err := evt.GetChatInfo(context.Background(), testMatrixText("group:88").Portal)
	if err != nil || info == nil || info.ParentID == nil || *info.ParentID != "space:7" || !info.CanBackfill {
		t.Fatalf("lost parent on metadata failure: %+v %v", info, err)
	}
}
func TestSpaceIdentityMatchesFrameworkParentKey(t *testing.T) {
	for _, split := range []bool{false, true} {
		gc := dmTestClient()
		gc.Main.br = &bridgev2.Bridge{Config: &bridgeconfig.BridgeConfig{SplitPortals: split}}
		key := gc.portalKeyForSpace("7")
		expected := networkid.PortalKey{ID: "space:7"}
		if split {
			expected.Receiver = "20"
		}
		if key != expected {
			t.Fatalf("split=%v: got %+v want %+v", split, key, expected)
		}
	}
}

func TestPeriodicTopicRefreshDoesNotRepeatForwardBackfill(t *testing.T) {
	gc, chat := discoverTestTopic(t, nil)
	periodic := gc.chatResync(chat, false)
	for _, lastID := range []networkid.MessageID{"101", "103"} {
		needed, err := periodic.CheckNeedsBackfill(context.Background(), &database.Message{ID: lastID})
		if err != nil || needed {
			t.Fatalf("metadata refresh requested history: %v %v", needed, err)
		}
	}
	reconnect := gc.chatResync(chat, true)
	needed, err := reconnect.CheckNeedsBackfill(context.Background(), &database.Message{ID: "101"})
	if err != nil || !needed {
		t.Fatalf("reconnect lost catch-up: %v %v", needed, err)
	}
	info, err := periodic.GetChatInfo(context.Background(), testMatrixText(chat.portalKey.ID).Portal)
	if err != nil || !info.CanBackfill {
		t.Fatalf("new topic lost initial history: %+v %v", info, err)
	}
}
