package connector

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/beeper/groupme-lib"
	"github.com/beeper/groupme/pkg/groupmeext"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
)

var _ bridgev2.RedactionHandlingNetworkAPI = (*GMClient)(nil)

func (gc *GMClient) HandleMatrixMessageRemove(ctx context.Context, msg *bridgev2.MatrixMessageRemove) error {
	if msg == nil || msg.Portal == nil || msg.TargetMessage == nil {
		return fmt.Errorf("missing GroupMe message deletion target")
	}
	target := msg.TargetMessage
	if target.Room != msg.Portal.PortalKey || string(target.SenderID) != gc.Meta.GMID || isPollVoteID(target.ID) {
		return fmt.Errorf("only your own messages in this GroupMe conversation can be deleted")
	}
	conversation, err := gc.conversationID(msg.Portal.ID)
	if err != nil {
		return err
	}
	return groupmeext.DeleteMessage(ctx, gc.Meta.Token, conversation, ParseMessageID(target.ID))
}

func messageDeletionEvent(key networkid.PortalKey, target groupme.ID, at time.Time, actor string) *simplevent.MessageRemove {
	if key.ID == "" || !nativeNumericID(target) || at.Unix() <= 0 {
		return nil
	}
	switch actor {
	case "sender", "admin", "system":
	default:
		return nil
	}
	// deletion_actor describes a role, not a user ID. Let the bridge bot redact.
	return &simplevent.MessageRemove{
		EventMeta:     simplevent.EventMeta{Type: bridgev2.RemoteEventMessageRemove, PortalKey: key, Timestamp: at},
		TargetMessage: MakeMessageID(target),
	}
}

// parseMessageDeletion consumes raw group-channel deletion notifications.
// The ordinary line.create path is handled by systemMessageDeletion instead.
func (gc *GMClient) parseMessageDeletion(msg groupme.PushMessage) (*simplevent.MessageRemove, bool) {
	typ, _ := msg.Data()["type"].(string)
	if typ != "message.deleted" {
		return nil, false
	}
	raw, err := json.Marshal(msg.Data()["subject"])
	if err != nil {
		return nil, true
	}
	var subject struct {
		ID        groupme.ID `json:"id"`
		GroupID   groupme.ID `json:"group_id"`
		DeletedAt string     `json:"deleted_at"`
		Actor     string     `json:"deletion_actor"`
	}
	if json.Unmarshal(raw, &subject) != nil || !nativeNumericID(subject.GroupID) || msg.Channel() != "/group/"+string(subject.GroupID) {
		return nil, true
	}
	at, err := time.Parse(time.RFC3339Nano, subject.DeletedAt)
	if err != nil {
		return nil, true
	}
	return messageDeletionEvent(gc.portalKeyForGroup(subject.GroupID), subject.ID, at, subject.Actor), true
}

// systemMessageDeletion targets event.data.message_id, never the system
// message's own ID. The bool also consumes malformed deletion notices.
func (gc *GMClient) systemMessageDeletion(msg *groupme.Message) (*simplevent.MessageRemove, bool) {
	if msg == nil || msg.Event == nil || msg.Event.Type != "message.deleted" {
		return nil, false
	}
	if !msg.System || msg.UserID != "system" || !nativeNumericID(msg.GroupID) {
		return nil, true
	}
	var data struct {
		MessageID groupme.ID `json:"message_id"`
		DeletedAt int64      `json:"deleted_at"`
		Actor     string     `json:"deletion_actor"`
	}
	if json.Unmarshal(msg.Event.Data, &data) != nil {
		return nil, true
	}
	return messageDeletionEvent(gc.portalKeyForGroup(msg.GroupID), data.MessageID, time.Unix(data.DeletedAt, 0), data.Actor), true
}
