package tui

import (
	"strings"
	"testing"

	alfredv1 "github.com/ArionMiles/alfred/gen/alfred/v1"
)

func TestTranscriptReplacesProvisionalOutputWithDurableMessage(t *testing.T) {
	t.Parallel()

	transcript := newTranscript()
	transcript.addUpdate(&alfredv1.RunUpdate{
		Payload: &alfredv1.RunUpdate_ContentDelta{ContentDelta: &alfredv1.RunContentDelta{
			Delta: &alfredv1.ContentPart{Value: &alfredv1.ContentPart_Text{Text: "hel"}},
		}},
	})
	if view := transcript.View(40, 4); !strings.Contains(view, "hel") {
		t.Fatalf("provisional view = %q", view)
	}

	transcript.addMessage(&alfredv1.Message{
		Role: alfredv1.MessageRole_MESSAGE_ROLE_ASSISTANT,
		Content: []*alfredv1.ContentPart{{
			Value: &alfredv1.ContentPart_Text{Text: "hello"},
		}},
	})
	view := transcript.View(40, 4)
	if strings.Contains(view, "hel\n") || !strings.Contains(view, "hello") {
		t.Fatalf("durable view = %q", view)
	}
}

func TestTranscriptDiscardsProvisionalOutputOnGap(t *testing.T) {
	t.Parallel()

	transcript := newTranscript()
	transcript.addUpdate(&alfredv1.RunUpdate{
		Payload: &alfredv1.RunUpdate_ContentDelta{ContentDelta: &alfredv1.RunContentDelta{
			Delta: &alfredv1.ContentPart{Value: &alfredv1.ContentPart_Text{Text: "partial secret"}},
		}},
	})
	transcript.markGap()

	view := transcript.View(80, 4)
	if strings.Contains(view, "partial secret") || !strings.Contains(view, "waiting for the completed message") {
		t.Fatalf("gap view = %q", view)
	}
}

func TestTranscriptFollowsOutputOnlyFromBottom(t *testing.T) {
	t.Parallel()

	transcript := newTranscript()
	transcript.setSize(20, 3)
	for _, text := range []string{"one", "two", "three", "four", "five"} {
		transcript.addMessage(textMessage(alfredv1.MessageRole_MESSAGE_ROLE_USER, text))
	}
	if !transcript.viewport.AtBottom() {
		t.Fatal("transcript did not follow initial output")
	}

	transcript.viewport.GotoTop()
	offset := transcript.viewport.YOffset()
	transcript.addMessage(textMessage(alfredv1.MessageRole_MESSAGE_ROLE_ASSISTANT, "six"))
	if transcript.viewport.YOffset() != offset {
		t.Fatalf("manual scroll offset = %d, want %d", transcript.viewport.YOffset(), offset)
	}

	transcript.viewport.GotoBottom()
	transcript.addMessage(textMessage(alfredv1.MessageRole_MESSAGE_ROLE_ASSISTANT, "seven"))
	if !transcript.viewport.AtBottom() {
		t.Fatal("transcript stopped following output from the bottom")
	}
}

func TestTranscriptDiscardsProvisionalOutputOnDisconnect(t *testing.T) {
	t.Parallel()

	transcript := newTranscript()
	transcript.addUpdate(&alfredv1.RunUpdate{
		Payload: &alfredv1.RunUpdate_ContentDelta{ContentDelta: &alfredv1.RunContentDelta{
			Delta: &alfredv1.ContentPart{Value: &alfredv1.ContentPart_Text{Text: "partial"}},
		}},
	})
	transcript.markDisconnected()

	view := transcript.View(80, 4)
	if strings.Contains(view, "partial") || !strings.Contains(view, "waiting for the completed message") {
		t.Fatalf("disconnected view = %q", view)
	}
}

func TestTranscriptRendersAssistantMarkdownAndHyperlinks(t *testing.T) {
	t.Parallel()

	transcript := newTranscript()
	transcript.addMessage(textMessage(
		alfredv1.MessageRole_MESSAGE_ROLE_ASSISTANT,
		"**Important**: read [the guide](https://example.com/guide).",
	))

	view := transcript.View(80, 6)
	if strings.Contains(view, "**Important**") {
		t.Fatalf("markdown syntax was not rendered: %q", view)
	}
	if !strings.Contains(view, "Important") || !strings.Contains(view, "\x1b]8;") {
		t.Fatalf("rendered markdown is missing text or OSC 8 hyperlink: %q", view)
	}
	if strings.Contains(view, "Assistant") {
		t.Fatalf("assistant role label remains in transcript: %q", view)
	}
}

func TestTranscriptHighlightsUserInputWithoutRenderingMarkdown(t *testing.T) {
	t.Parallel()

	transcript := newTranscript()
	transcript.addMessage(textMessage(
		alfredv1.MessageRole_MESSAGE_ROLE_USER,
		"Please keep **these stars** literal.",
	))

	view := transcript.View(80, 4)
	if !strings.Contains(view, "┃") || !strings.Contains(view, "**these stars**") {
		t.Fatalf("user message is not a distinct literal block: %q", view)
	}
	if strings.Contains(view, "You") {
		t.Fatalf("user role label remains in transcript: %q", view)
	}
}
