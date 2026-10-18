package groupmeext

import (
	"context"
	"fmt"
	"net/http"

	"github.com/beeper/groupme-lib"
)

type Client struct {
	*groupme.Client
	token string
}

// NewClient creates a new GroupMe API Client
func NewClient(authToken string) *Client {
	n := Client{
		Client: groupme.NewClient(authToken),
		token:  authToken,
	}
	return &n
}
func (c Client) IndexAllGroups(ctx context.Context) ([]*Group, error) {
	var groups []*Group
	for page := 1; ; page++ {
		var resp struct {
			Response []*Group `json:"response"`
		}
		err := activityRequest(ctx, c.token, http.MethodGet, fmt.Sprintf("/v3/groups?page=%d&per_page=100", page), nil, &resp)
		batch := resp.Response
		if err != nil {
			return nil, err
		}
		groups = append(groups, batch...)
		if len(batch) < 100 {
			return groups, nil
		}
	}
}

func (c Client) IndexAllChats(ctx context.Context) ([]*groupme.Chat, error) {
	var chats []*groupme.Chat
	for page := 1; ; page++ {
		batch, err := c.IndexChats(ctx, &groupme.IndexChatsQuery{Page: page, PerPage: 100})
		if err != nil {
			return nil, err
		}
		chats = append(chats, batch...)
		if len(batch) < 100 {
			return chats, nil
		}
	}
}
