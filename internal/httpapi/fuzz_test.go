package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vanshamara/Augur/internal/core"
)

// FuzzChatCompletions checks that the chat endpoint never panics on arbitrary
// request bodies and always writes an HTTP response.
func FuzzChatCompletions(f *testing.F) {
	f.Add(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	f.Add(`{"model":"m","messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)
	f.Add(`{"model":"m"}`)
	f.Add(`{}`)
	f.Add(`{"model":"m","messages":[]}`)
	f.Add(`garbage`)
	f.Add(``)

	f.Fuzz(func(t *testing.T, body string) {
		server := testServer(t, &fakeGateway{resp: core.Response{Backend: "b", OutputText: "ok"}})
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		rec := httptest.NewRecorder()

		server.ServeHTTP(rec, req)
		if rec.Code == 0 {
			t.Fatalf("no status written for body %q", body)
		}
	})
}
