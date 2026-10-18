package connector

import (
	"context"
	"encoding/json"
	"time"

	"github.com/beeper/groupme-lib"
	"github.com/beeper/groupme/pkg/groupmeext"
	"github.com/rs/zerolog"
	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/simplevent"
)

func (gc *GMClient) spaceChatInfo(ctx context.Context, parent *groupme.Group) *bridgev2.ChatInfo {
	info := gc.groupChatInfo(ctx, parent)
	info.Type = ptr.Ptr(database.RoomTypeSpace)
	info.CanBackfill = false
	return info
}

func (gc *GMClient) topicChatInfo(ctx context.Context, parent *groupme.Group, topic *groupmeext.Topic) *bridgev2.ChatInfo {
	info := gc.groupChatInfo(ctx, parent)
	info.Name = ptr.Ptr(parent.Name + " · " + topic.Name)
	info.Topic = ptr.Ptr(topic.Description)
	info.Avatar = gc.avatarFor(ctx, topic.AvatarURL)
	info.ParentID = ptr.Ptr(MakeSpacePortalID(parent.ID))
	return info
}

func (gc *GMClient) fetchTopicChatInfo(ctx context.Context, parent, topicID groupme.ID) (*bridgev2.ChatInfo, error) {
	group, err := gc.Client.ShowGroup(ctx, parent)
	if err != nil {
		return nil, err
	}
	topic, err := gc.Client.ShowTopic(ctx, parent, topicID)
	if err != nil {
		return nil, err
	}
	return gc.topicChatInfo(ctx, &group.Group, topic), nil
}

// Snapshots are captured by the queued resync. The framework persists ParentID
// as ParentKey, which is also the metadata lookup context after a restart.
func (gc *GMClient) listGroupChats(ctx context.Context, topicsOnly bool) []nativeChat {
	groups, err := gc.Client.IndexAllGroups(ctx)
	if err != nil {
		zerolog.Ctx(ctx).Err(err).Msg("Failed to list GroupMe groups")
		return nil
	}
	var chats []nativeChat
	for _, group := range groups {
		if group == nil || group.ID == "" {
			continue
		}
		if !topicsOnly || group.ChildrenCount > 0 {
			chats = append(chats, nativeChat{portalKey: gc.portalKeyForGroup(group.ID), latestID: group.Messages.LastMessageID,
				chatInfo: func(ctx context.Context, _ *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
					info := gc.groupChatInfo(ctx, &group.Group)
					if group.ChildrenCount > 0 {
						info.ParentID = ptr.Ptr(MakeSpacePortalID(group.ID))
					}
					return info, nil
				}})
		}
		if group.ChildrenCount == 0 {
			continue
		}
		chats = append(chats, nativeChat{portalKey: gc.portalKeyForSpace(group.ID), chatInfo: func(ctx context.Context, _ *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
			return gc.spaceChatInfo(ctx, &group.Group), nil
		}})
		topics, err := gc.Client.IndexTopics(ctx, group.ID)
		if err != nil {
			zerolog.Ctx(ctx).Err(err).Msg("Failed to list GroupMe topics")
			continue
		}
		for _, topic := range topics {
			chats = append(chats, nativeChat{portalKey: gc.portalKeyForGroup(topic.ID), latestID: topic.Messages.LastMessageID,
				chatInfo: func(ctx context.Context, _ *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
					return gc.topicChatInfo(ctx, &group.Group, &topic), nil
				}})
		}
	}
	return chats
}

// groupme-lib drops parent_id. Preserve it before dispatch so a live-first
// topic can obtain metadata even when it wasn't present at initial discovery.
func (gc *GMClient) topicPushResync(msg groupme.PushMessage) *simplevent.ChatResync {
	raw, err := json.Marshal(msg.Data()["subject"])
	if err != nil {
		return nil
	}
	var subject struct {
		GroupID  groupme.ID `json:"group_id"`
		ParentID groupme.ID `json:"parent_id"`
	}
	if json.Unmarshal(raw, &subject) != nil || subject.GroupID == "" || subject.ParentID == "" || subject.GroupID == subject.ParentID || !nativeNumericID(subject.GroupID) || !nativeNumericID(subject.ParentID) {
		return nil
	}
	if msg.Channel() != "/group/"+string(subject.GroupID) && msg.Channel() != groupme.UserChannel(groupme.ID(gc.Meta.GMID)) {
		return nil
	}
	parent := MakeSpacePortalID(subject.ParentID)
	return &simplevent.ChatResync{
		EventMeta: simplevent.EventMeta{Type: bridgev2.RemoteEventChatResync, PortalKey: gc.portalKeyForGroup(subject.GroupID), CreatePortal: true, Timestamp: time.Now()},
		GetChatInfoFunc: func(ctx context.Context, portal *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
			if portal.ParentKey.ID == parent && portal.MXID != "" {
				return nil, nil
			}
			info, err := gc.fetchTopicChatInfo(ctx, subject.ParentID, subject.GroupID)
			if err == nil {
				return info, nil
			}
			// Keep the authenticated push's parent context even if metadata
			// is temporarily unavailable. Discovery replaces this placeholder.
			zerolog.Ctx(ctx).Warn().Err(err).Msg("Topic metadata unavailable; preserving parent link")
			return &bridgev2.ChatInfo{Name: ptr.Ptr("Topic " + string(subject.GroupID)), ParentID: &parent, Type: ptr.Ptr(database.RoomTypeDefault), CanBackfill: true}, nil
		},
	}
}

// A topic may be created without a message on any subscribed channel. Refresh
// sequentially, independent of reconnects, with no per-message inventory scans.
func (gc *GMClient) refreshTopics(ctx context.Context) {
	gc.queueChats(ctx, gc.listGroupChats(ctx, true))
}
