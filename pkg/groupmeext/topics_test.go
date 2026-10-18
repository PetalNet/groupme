package groupmeext

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestTopicIDsDecodeWithoutPrecisionLoss(t *testing.T) {
	for _, raw := range []string{`{"id":9007199254740993,"parent_id":7,"topic":"Plans"}`, `{"id":"9007199254740993","parent_id":"7","topic":"Plans"}`} {
		var topic Topic
		if err := json.Unmarshal([]byte(raw), &topic); err != nil || topic.ID != "9007199254740993" || topic.ParentID != "7" {
			t.Fatalf("topic identity lost: %+v %v", topic, err)
		}
	}
	for _, raw := range []string{`{"id":null,"parent_id":7}`, `{"id":1.5,"parent_id":7}`, `{"id":88,"parent_id":0}`, `{"id":"88/other","parent_id":7}`} {
		var topic Topic
		if json.Unmarshal([]byte(raw), &topic) == nil {
			t.Fatalf("accepted malformed identity: %s", raw)
		}
	}
}

func TestTopicMetadataValidatesParentAndIdentity(t *testing.T) {
	for _, payload := range []string{`{"id":88,"parent_id":8}`, `{"id":89,"parent_id":7}`, `{"id":88,"parent_id":7}`} {
		t.Run(payload, func(t *testing.T) {
			prev := http.DefaultTransport
			t.Cleanup(func() { http.DefaultTransport = prev })
			http.DefaultTransport = roundTripper(func(r *http.Request) (*http.Response, error) {
				if r.Method != "GET" || r.URL.Path != "/v3/groups/7/subgroups/88" || r.Header.Get("X-Access-Token") != "test-token" || r.URL.Query().Has("token") {
					t.Fatal("incorrect subgroup metadata request")
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"response":` + payload + `}`))}, nil
			})
			client := NewClient("test-token")
			defer client.Close()
			_, err := client.ShowTopic(context.Background(), "7", "88")
			if (err == nil) != (payload == `{"id":88,"parent_id":7}`) {
				t.Fatalf("incorrect metadata acceptance: %v", err)
			}
		})
	}
}
