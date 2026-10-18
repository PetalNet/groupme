package connector

import (
	"context"
	"net/http"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

func TestChatInfoEnablesInitialBackfill(t *testing.T) {
	for _, portalID := range []networkid.PortalID{"group:7", "dm:9"} {
		t.Run(string(portalID), func(t *testing.T) {
			mockGroupMe(t, func(r *http.Request) (int, string) {
				switch r.URL.Path {
				case "/v3/groups/7":
					return 200, `{"response":{"id":"7","name":"Group","members":[{"user_id":"9","nickname":"Member nickname"}]},"meta":{"code":200}}`
				case "/v3/chats":
					if r.URL.Query().Get("page") == "2" {
						return 200, `{"response":[],"meta":{"code":200}}`
					}
					return 200, `{"response":[{"other_user":{"id":"9","name":"Partner name"}}],"meta":{"code":200}}`
				default:
					return 200, `{"response":{},"meta":{"code":200}}`
				}
			})
			info, err := dmTestClient().GetChatInfo(context.Background(), testMatrixText(portalID).Portal)
			if err != nil {
				t.Fatal(err)
			}
			if !info.CanBackfill {
				t.Error("new portal cannot backfill before queued reactions and edits")
			}
			member := info.Members.MemberMap["9"]
			if member.UserInfo == nil || member.UserInfo.Name == nil || *member.UserInfo.Name == "" {
				t.Fatal("member profile has no name")
			}
		})
	}
}

func TestGetUserInfoPreservesKnownNameWhenContactNameMissing(t *testing.T) {
	mockGroupMe(t, func(r *http.Request) (int, string) {
		return 200, `{"response":[{"id":"9","name":"","avatar_url":"https://i.groupme.com/avatar"}],"meta":{"code":200}}`
	})
	ghost := &bridgev2.Ghost{Ghost: &database.Ghost{ID: "9", Name: "Known member", NameSet: true}}
	info, err := dmTestClient().GetUserInfo(context.Background(), ghost)
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != nil && *info.Name != "Known member" {
		t.Fatalf("known name would be overwritten with %q", *info.Name)
	}
	if info.Avatar == nil {
		t.Fatal("lost available avatar while preserving name")
	}
}

func TestIncompleteProfilesDoNotClearNames(t *testing.T) {
	for _, kind := range []string{"self", "contact list", "dm"} {
		t.Run(kind, func(t *testing.T) {
			mockGroupMe(t, func(r *http.Request) (int, string) {
				switch r.URL.Path {
				case "/v4/relationships":
					if kind == "self" {
						return 200, `{"response":[],"meta":{"code":200}}`
					}
					return 200, `{"response":[{"id":"9"}],"meta":{"code":200}}`
				case "/v3/users/me":
					return 200, `{"response":{"id":"20"},"meta":{"code":200}}`
				default:
					return 200, `{"response":{},"meta":{"code":200}}`
				}
			})
			gc := dmTestClient()
			var info *bridgev2.UserInfo
			switch kind {
			case "self":
				var err error
				info, err = gc.GetUserInfo(context.Background(), &bridgev2.Ghost{Ghost: &database.Ghost{ID: "20", Name: "Known self"}})
				if err != nil {
					t.Fatal(err)
				}
			case "contact list":
				contacts, err := gc.GetContactList(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				info = contacts[0].UserInfo
			case "dm":
				chat := gc.dmChatInfo(context.Background(), "9", nil)
				info = chat.Members.MemberMap["9"].UserInfo
			}
			if info == nil || info.Name != nil {
				t.Fatalf("incomplete profile would clear name: %+v", info)
			}
		})
	}
}
