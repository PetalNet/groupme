package groupmeext

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/beeper/groupme-lib"
)

// DeleteMessage removes a message for everyone. DMs use the canonical a+b
// conversation ID; group and topic conversations use their native group ID.
func DeleteMessage(ctx context.Context, token string, conversation, message groupme.ID) error {
	parts := strings.Split(string(conversation), "+")
	if len(parts) > 2 || !decimalMessageID(string(message)) {
		return fmt.Errorf("invalid GroupMe message deletion target")
	}
	for _, part := range parts {
		if !decimalMessageID(part) {
			return fmt.Errorf("invalid GroupMe deletion conversation")
		}
	}
	return activityRequest(ctx, token, http.MethodDelete, "/v3/conversations/"+string(conversation)+"/messages/"+string(message), nil, nil)
}

func decimalMessageID(id string) bool {
	if id == "" {
		return false
	}
	for _, c := range id {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
