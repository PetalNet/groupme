package connector

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/beeper/groupme-lib"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

const topicParentFixture = `{"id":"7","name":"Friends","description":"Parent description","children_count":1,"members":[]}`
const topicFixture = `{"id":88,"parent_id":7,"topic":"Plans","description":"Weekend plans","avatar_url":"","messages":{"last_message_id":"103"}}`

func discoverTestTopic(t *testing.T, outgoing func(*http.Request) (int, string)) (*GMClient, nativeChat) {
	t.Helper()
	mockGroupMe(t, func(r *http.Request) (int, string) {
		switch r.URL.Path {
		case "/v3/groups":
			return 200, `{"response":[` + topicParentFixture + `]}`
		case "/v3/groups/7/subgroups":
			return 200, `{"response":[` + topicFixture + `]}`
		case "/v3/groups/7":
			return 200, `{"response":` + topicParentFixture + `}`
		case "/v3/groups/7/subgroups/88":
			return 200, `{"response":` + topicFixture + `}`
		case "/v3/chats":
			return 200, `{"response":[]}`
		default:
			if outgoing != nil {
				return outgoing(r)
			}
			t.Fatalf("unexpected topic endpoint: %s", r.URL.Path)
			return 500, `{}`
		}
	})
	gc := dmTestClient()
	for _, chat := range gc.listChats(context.Background()) {
		if chat.portalKey.ID == "group:88" {
			return gc, chat
		}
	}
	t.Fatal("subgroup missing from discovery")
	return nil, nativeChat{}
}

func TestTopicIDMappingAndParentSpace(t *testing.T) {
	gc, chat := discoverTestTopic(t, nil)
	ctx := context.Background()
	if chat.portalKey != (networkid.PortalKey{ID: "group:88", Receiver: "20"}) || chat.latestID != "103" {
		t.Fatalf("wrong topic identity: %+v", chat)
	}
	topicPortal := testMatrixText(chat.portalKey.ID).Portal
	topicPortal.ParentKey = networkid.PortalKey{ID: "space:7"}
	info, err := gc.GetChatInfo(ctx, topicPortal)
	if err != nil || *info.Name != "Friends · Plans" || *info.Topic != "Weekend plans" || *info.ParentID != "space:7" || !info.CanBackfill || *info.Type != database.RoomTypeDefault {
		t.Fatalf("wrong topic info: %+v %v", info, err)
	}
	// ParentKey is stored by bridgev2. A newly loaded client must use it rather
	// than the nonexistent /groups/88 metadata endpoint.
	restarted := dmTestClient()
	portal := testMatrixText("group:88").Portal
	portal.ParentKey = networkid.PortalKey{ID: *info.ParentID, Receiver: "20"}
	refreshed, err := restarted.GetChatInfo(ctx, portal)
	if err != nil || *refreshed.Name != *info.Name {
		t.Fatalf("restart metadata: %+v %v", refreshed, err)
	}
	space, err := gc.GetChatInfo(ctx, testMatrixText("space:7").Portal)
	if err != nil || *space.Type != database.RoomTypeSpace || space.CanBackfill || space.ParentID != nil {
		t.Fatalf("invalid space: %+v %v", space, err)
	}
	parent, err := gc.GetChatInfo(ctx, testMatrixText("group:7").Portal)
	if err != nil || *parent.ParentID != "space:7" || !parent.CanBackfill {
		t.Fatalf("parent chat lost: %+v %v", parent, err)
	}
}

func TestIncomingTopicMessageRoutesToDiscoveredPortal(t *testing.T) {
	gc, chat := discoverTestTopic(t, nil)
	var msg groupme.Message
	if err := json.Unmarshal([]byte(`{"id":"101","group_id":"88","parent_id":"7","user_id":"9","created_at":101,"text":"hello"}`), &msg); err != nil {
		t.Fatal(err)
	}
	evt := gc.makeRemoteMessage(msg)
	if evt.PortalKey != chat.portalKey || evt.PortalKey == gc.portalKeyForGroup("7") {
		t.Fatalf("topic message routed to %+v", evt.PortalKey)
	}

}

func TestOutgoingTopicSendUsesSubgroupID(t *testing.T) {
	sent := false
	gc, chat := discoverTestTopic(t, func(r *http.Request) (int, string) {
		if r.Method != "POST" || r.URL.Path != "/v3/groups/88/messages" {
			t.Fatalf("send escaped topic: %s %s", r.Method, r.URL.Path)
		}
		var payload struct {
			Message groupme.Message `json:"message"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.Message.Text != "DM test" || len(payload.Message.Attachments) != 1 || payload.Message.Attachments[0].ReplyID != "100" {
			t.Fatalf("lost topic reply: %+v", payload.Message)
		}
		sent = true
		return 201, `{"response":{"message":{"id":"104","group_id":"88","user_id":"20","created_at":104}},"meta":{"code":201}}`
	})
	msg := testMatrixText(chat.portalKey.ID)
	msg.ReplyTo = &database.Message{ID: "100", Room: chat.portalKey}
	if _, err := gc.HandleMatrixMessage(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if !sent {
		t.Fatal("no topic send")
	}
}

func TestTopicListingFailurePreservesOtherChats(t *testing.T) {
	mockGroupMe(t, func(r *http.Request) (int, string) {
		switch r.URL.Path {
		case "/v3/groups":
			return 200, `{"response":[` + topicParentFixture + `,{"id":"8","name":"Other"}]}`
		case "/v3/groups/7/subgroups":
			return 429, `{}`
		case "/v3/chats":
			return 200, `{"response":[]}`
		default:
			t.Fatalf("unexpected request %s", r.URL.Path)
			return 500, `{}`
		}
	})
	chats := dmTestClient().listChats(context.Background())
	seen := map[networkid.PortalID]bool{}
	for _, chat := range chats {
		seen[chat.portalKey.ID] = true
	}
	if !seen["group:7"] || !seen["group:8"] {
		t.Fatal("topic failure dropped ordinary groups")
	}
}
