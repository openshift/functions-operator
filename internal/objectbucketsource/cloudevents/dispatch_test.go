package cloudevents

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDispatchEvent(t *testing.T) {
	var gotHeaders http.Header
	var gotBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		var err error
		gotBody, err = io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading request body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	records := []json.RawMessage{
		json.RawMessage(`{"eventName":"s3:ObjectCreated:Put","s3":{"bucket":{"name":"my-bucket"},"object":{"key":"my-key"}}}`),
	}

	err := DispatchEvent(context.Background(), srv.URL, "my-bucket", records)
	if err != nil {
		t.Fatalf("DispatchEvent returned error: %v", err)
	}

	t.Logf("=== Headers ===")
	for k, vals := range gotHeaders {
		for _, v := range vals {
			t.Logf("  %s: %s", k, v)
		}
	}

	t.Logf("=== Body (raw) ===")
	t.Logf("%s", gotBody)

	t.Logf("=== Body (pretty) ===")
	var pretty json.RawMessage
	if err := json.Unmarshal(gotBody, &pretty); err == nil {
		formatted, _ := json.MarshalIndent(pretty, "  ", "  ")
		t.Logf("%s", formatted)
	}
}
