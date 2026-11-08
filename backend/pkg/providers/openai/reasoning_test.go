package openai

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ArionMiles/alfred"
)

func TestStreamReadableReasoningParts(t *testing.T) {
	t.Parallel()
	for _, kind := range []alfred.ReasoningTextKind{alfred.ReasoningTextSummary, alfred.ReasoningTextReasoning} {
		t.Run(string(kind), func(t *testing.T) {
			eventType, indexKey := "response.reasoning_summary_text.delta", "summary_index"
			if kind == alfred.ReasoningTextReasoning {
				eventType, indexKey = "response.reasoning_text.delta", "content_index"
			}
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				for index, text := range []string{"Inspect ", "the file", "Then answer"} {
					part := 0
					if index == 2 {
						part = 1
					}
					writeSSE(t, writer, fmt.Sprintf(`{"type":%q,"output_index":2,%q:%d,"delta":%q}`, eventType, indexKey, part, text))
				}
				writeSSE(t, writer, `{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[`+reasoningJSON+`]}}`)
			}))
			t.Cleanup(server.Close)
			events, err := collect(t.Context(), testProvider(server), testRequest())
			if err != nil || len(events) != 5 {
				t.Fatalf("events=%v error=%v", events, err)
			}
			for index, want := range []uint32{0, 0, 1} {
				delta, ok := events[index].Payload.(alfred.ModelReasoningDelta)
				if !ok || delta.Index != want || delta.TextKind != kind || delta.Text == "" || len(events[index].Native) != 0 {
					t.Fatalf("delta=%#v", events[index])
				}
			}
		})
	}
}
