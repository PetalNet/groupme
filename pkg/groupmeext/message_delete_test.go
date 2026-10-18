package groupmeext

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/beeper/groupme-lib"
)

func TestDeleteMessageNativeRequest(t *testing.T) {
	previous := activityHTTPClient
	t.Cleanup(func() { activityHTTPClient = previous })
	for _, conversation := range []groupme.ID{"88", "9+20"} {
		for _, status := range []int{200, 204, 403, 429} {
			activityHTTPClient = &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodDelete || r.URL.EscapedPath() != "/v3/conversations/"+string(conversation)+"/messages/123" || r.URL.RawQuery != "" || r.Header.Get("X-Access-Token") != "secret-token" {
					t.Fatal("incorrect deletion endpoint or authentication")
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("secret-provider-body"))}, nil
			})}
			err := DeleteMessage(context.Background(), "secret-token", conversation, "123")
			if (err != nil) != (status >= 300) {
				t.Fatalf("status %d returned %v", status, err)
			}
			if err != nil && (strings.Contains(err.Error(), "secret-token") || strings.Contains(err.Error(), "secret-provider-body")) {
				t.Fatal("error exposed credentials or provider body")
			}
		}
	}
}

func TestDeleteMessageRejectsInvalidIDs(t *testing.T) {
	previous := activityHTTPClient
	t.Cleanup(func() { activityHTTPClient = previous })
	activityHTTPClient = &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
		t.Fatal("invalid target made a request")
		return nil, nil
	})}
	for _, pair := range [][2]groupme.ID{{"", "1"}, {"1", ""}, {"1/2", "3"}, {"1+2+3", "4"}, {"1+", "2"}, {"1", "../2"}} {
		if DeleteMessage(context.Background(), "secret", pair[0], pair[1]) == nil {
			t.Fatalf("accepted invalid target %v", pair)
		}
	}
}
