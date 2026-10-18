package connector

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"testing"

	"github.com/rs/zerolog"
	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

type historyMatrix struct{ bridgev2.MatrixConnector }

func (historyMatrix) Init(*bridgev2.Bridge)                           {}
func (historyMatrix) BotIntent() bridgev2.MatrixAPI                   { return &historyIntent{} }
func (historyMatrix) GhostIntent(networkid.UserID) bridgev2.MatrixAPI { return &historyIntent{} }

type historyIntent struct{ bridgev2.MatrixAPI }

func TestFetchMessagesChronologicalResponse(t *testing.T) {
	for _, portalID := range []networkid.PortalID{"group:7", "dm:9"} {
		for _, forward := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/forward=%v", portalID, forward), func(t *testing.T) {
				ctx := context.Background()
				db, err := dbutil.NewWithDialect(filepath.Join(t.TempDir(), "history.db")+"?_foreign_keys=on", "sqlite3")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = db.Close() })
				gc := dmTestClient()
				br := bridgev2.NewBridge("", db, zerolog.Nop(), nil, historyMatrix{}, gc.Main, func(*bridgev2.Bridge) bridgev2.CommandProcessor { return nil })
				if err = br.DB.Upgrade(ctx); err != nil {
					t.Fatal(err)
				}
				portal := testMatrixText(portalID).Portal
				portal.Bridge = br
				mockGroupMe(t, func(r *http.Request) (int, string) {
					if forward && portalID == "group:7" && r.URL.Query().Get("after_id") != "100" {
						t.Fatal("forward group history must use after_id anchor")
					}
					field, identity := "messages", `"group_id":"7"`
					if portalID == "dm:9" {
						field, identity = "direct_messages", `"chat_id":"9+20"`
					}
					return 200, fmt.Sprintf(`{"response":{"%s":[{"id":"103","user_id":"system",%s,"created_at":103,"text":"third"},{"id":"101","user_id":"system",%s,"created_at":101,"text":"first"},{"id":"102","user_id":"system",%s,"created_at":102,"text":"second"}]},"meta":{"code":200}}`, field, identity, identity, identity)
				})
				params := bridgev2.FetchMessagesParams{Portal: portal, Count: 3, Forward: forward}
				if forward {
					params.AnchorMessage = &database.Message{ID: "100"}
				}
				resp, err := gc.FetchMessages(ctx, params)
				if err != nil {
					t.Fatal(err)
				}
				var ids []networkid.MessageID
				for i, m := range resp.Messages {
					if m.Timestamp.Unix() != int64(101+i) {
						t.Fatalf("timestamp at index %d is %d", i, m.Timestamp.Unix())
					}
					ids = append(ids, m.ID)
				}
				if !slices.Equal(ids, []networkid.MessageID{"101", "102", "103"}) {
					t.Fatalf("history is not chronological: %v", ids)
				}
				wantCursor := networkid.PaginationCursor("101")
				if forward && portalID == "group:7" {
					wantCursor = ""
				}
				if resp.Forward != forward || resp.Cursor != wantCursor || resp.HasMore != (portalID == "group:7") {
					t.Fatalf("incorrect pagination response: %+v", resp)
				}
			})
		}
	}
}
