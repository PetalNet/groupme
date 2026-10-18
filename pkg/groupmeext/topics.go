package groupmeext

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/beeper/groupme-lib"
)

// Group adds the discovery hint omitted by groupme-lib.
type Group struct {
	groupme.Group
	ChildrenCount int `json:"children_count"`
}

type Topic struct {
	ID          groupme.ID
	ParentID    groupme.ID
	Name        string
	Description string
	AvatarURL   string
	Messages    groupme.GroupMessages
}

// Subgroup IDs are JSON numbers, unlike group/message IDs. Accept either
// representation without converting through float64 and losing precision.
func (t *Topic) UnmarshalJSON(data []byte) error {
	var wire struct {
		ID          json.Number           `json:"id"`
		ParentID    json.Number           `json:"parent_id"`
		Name        string                `json:"topic"`
		Description string                `json:"description"`
		AvatarURL   string                `json:"avatar_url"`
		Messages    groupme.GroupMessages `json:"messages"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return fmt.Errorf("invalid GroupMe topic")
	}
	for _, id := range []json.Number{wire.ID, wire.ParentID} {
		n, err := strconv.ParseUint(string(id), 10, 64)
		if err != nil || n == 0 {
			return fmt.Errorf("invalid GroupMe topic ID")
		}
	}
	*t = Topic{ID: groupme.ID(wire.ID), ParentID: groupme.ID(wire.ParentID), Name: wire.Name, Description: wire.Description, AvatarURL: wire.AvatarURL, Messages: wire.Messages}
	return nil
}

func (c Client) ShowGroup(ctx context.Context, id groupme.ID) (*Group, error) {
	var resp struct {
		Response Group `json:"response"`
	}
	err := activityRequest(ctx, c.token, http.MethodGet, "/v3/groups/"+url.PathEscape(string(id)), nil, &resp)
	if err != nil {
		return nil, err
	}
	if resp.Response.ID != id {
		return nil, fmt.Errorf("GroupMe returned invalid group metadata")
	}
	return &resp.Response, nil
}

func (c Client) IndexTopics(ctx context.Context, parent groupme.ID) ([]Topic, error) {
	var resp struct {
		Response []Topic `json:"response"`
	}
	err := activityRequest(ctx, c.token, http.MethodGet, "/v3/groups/"+url.PathEscape(string(parent))+"/subgroups", nil, &resp)
	if err != nil {
		return nil, err
	}
	for _, topic := range resp.Response {
		if topic.ParentID != parent || topic.ID == parent {
			return nil, fmt.Errorf("GroupMe returned a topic from another parent")
		}
	}
	return resp.Response, nil
}

func (c Client) ShowTopic(ctx context.Context, parent, id groupme.ID) (*Topic, error) {
	var resp struct {
		Response Topic `json:"response"`
	}
	err := activityRequest(ctx, c.token, http.MethodGet, "/v3/groups/"+url.PathEscape(string(parent))+"/subgroups/"+url.PathEscape(string(id)), nil, &resp)
	if err != nil {
		return nil, err
	}
	if resp.Response.ID != id || resp.Response.ParentID != parent || id == parent {
		return nil, fmt.Errorf("GroupMe returned invalid topic metadata")
	}
	return &resp.Response, nil
}
