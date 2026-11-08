package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/glamour/v2/ansi"
	"charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2"

	alfredv1 "github.com/ArionMiles/alfred/gen/alfred/v1"
)

type transcriptEntry struct {
	role      alfredv1.MessageRole
	content   string
	reasoning alfredv1.ReasoningTextKind
	callID    string
	index     uint32
}

// transcript owns durable conversation rendering and provisional model output.
type transcript struct {
	viewport       viewport.Model
	entries        []transcriptEntry
	provisional    []transcriptEntry
	completedCalls map[string]bool
	gap            bool
}

func newTranscript() transcript {
	view := viewport.New(viewport.WithWidth(80), viewport.WithHeight(1))
	view.SoftWrap = true
	view.FillHeight = true
	return transcript{viewport: view}
}

func (transcript transcript) Update(message tea.Msg) (transcript, tea.Cmd) {
	var command tea.Cmd
	transcript.viewport, command = transcript.viewport.Update(message)
	return transcript, command
}

func (transcript transcript) View(width, height int) string {
	view := transcript.viewport
	view.SetWidth(max(width, 1))
	view.SetHeight(max(height, 1))
	if view.PastBottom() {
		view.GotoBottom()
	}
	return view.View()
}

func (transcript *transcript) setSize(width, height int) {
	wasAtBottom := transcript.viewport.AtBottom()
	widthChanged := transcript.viewport.Width() != max(width, 1)
	transcript.viewport.SetWidth(max(width, 1))
	transcript.viewport.SetHeight(max(height, 1))
	if widthChanged {
		transcript.render(wasAtBottom)
		return
	}
	if wasAtBottom || transcript.viewport.PastBottom() {
		transcript.viewport.GotoBottom()
	}
}

func (transcript *transcript) addMessage(message *alfredv1.Message) {
	if message == nil {
		return
	}
	transcript.entries = append(transcript.entries, transcriptEntry{
		role:    message.GetRole(),
		content: contentText(message.GetContent()),
	})
	if message.GetRole() == alfredv1.MessageRole_MESSAGE_ROLE_ASSISTANT {
		transcript.provisional = nil
		transcript.gap = false
	}
	transcript.refresh()
}

func (transcript *transcript) addToolActivity(text string) {
	transcript.entries = append(transcript.entries, transcriptEntry{content: text})
	transcript.refresh()
}

func (transcript *transcript) addUpdate(update *alfredv1.RunUpdate) {
	var entry transcriptEntry
	switch payload := update.GetPayload().(type) {
	case *alfredv1.RunUpdate_ContentDelta:
		entry = transcriptEntry{
			role:    alfredv1.MessageRole_MESSAGE_ROLE_ASSISTANT,
			content: payload.ContentDelta.GetDelta().GetText(), callID: payload.ContentDelta.GetModelCallId(), index: payload.ContentDelta.GetIndex(),
		}
	case *alfredv1.RunUpdate_ReasoningDelta:
		entry = transcriptEntry{
			content: payload.ReasoningDelta.GetText(), reasoning: payload.ReasoningDelta.GetTextKind(),
			callID: payload.ReasoningDelta.GetModelCallId(), index: payload.ReasoningDelta.GetIndex(),
		}
	}
	if entry.content == "" {
		return
	}
	if transcript.completedCalls[entry.callID] {
		return
	}
	last := len(transcript.provisional) - 1
	if last >= 0 && transcript.provisional[last].callID == entry.callID && transcript.provisional[last].index == entry.index &&
		transcript.provisional[last].role == entry.role && transcript.provisional[last].reasoning == entry.reasoning {
		transcript.provisional[last].content += entry.content
	} else {
		transcript.provisional = append(transcript.provisional, entry)
	}
	transcript.gap = false
	transcript.refresh()
}

func (transcript *transcript) addModelOutput(output *alfredv1.ModelOutputCompleted) {
	if transcript.completedCalls == nil {
		transcript.completedCalls = make(map[string]bool)
	}
	transcript.completedCalls[output.GetModelCallId()] = true
	transcript.provisional = nil
	transcript.gap = false
	for _, item := range output.GetOutput() {
		if message := item.GetMessage(); message != nil {
			transcript.entries = append(transcript.entries, transcriptEntry{role: message.GetRole(), content: contentText(message.GetContent())})
		} else if reasoning := item.GetReasoning(); reasoning != nil && reasoning.GetText() != "" {
			transcript.entries = append(transcript.entries, transcriptEntry{content: reasoning.GetText(), reasoning: reasoning.GetTextKind()})
		}
	}
	transcript.refresh()
}

func (transcript *transcript) markGap() {
	transcript.provisional = nil
	transcript.gap = true
	transcript.refresh()
}

func (transcript *transcript) markDisconnected() {
	if len(transcript.provisional) != 0 {
		transcript.markGap()
	}
}

func (transcript *transcript) discardProvisional() {
	if len(transcript.provisional) == 0 && !transcript.gap {
		return
	}
	transcript.provisional = nil
	transcript.gap = false
	transcript.refresh()
}

func (transcript *transcript) refresh() {
	transcript.render(transcript.viewport.AtBottom())
}

func (transcript *transcript) render(followOutput bool) {
	renderer, rendererErr := newMarkdownRenderer(transcript.viewport.Width())
	sections := make([]string, 0, len(transcript.entries)+1)
	for _, entries := range [][]transcriptEntry{transcript.entries, transcript.provisional} {
		for _, entry := range entries {
			sections = append(sections, renderTranscriptEntry(
				renderer,
				rendererErr,
				transcript.viewport.Width(),
				entry,
			))
		}
	}
	if transcript.gap {
		sections = append(sections, lipgloss.NewStyle().Faint(true).Italic(true).Render(
			"Streaming output was interrupted; waiting for the completed message…",
		))
	}
	transcript.viewport.SetContent(strings.Join(sections, "\n\n"))
	if followOutput {
		transcript.viewport.GotoBottom()
	}
}

func renderTranscriptEntry(
	renderer *glamour.TermRenderer,
	rendererErr error,
	width int,
	entry transcriptEntry,
) string {
	if entry.reasoning != alfredv1.ReasoningTextKind_REASONING_TEXT_KIND_UNSPECIFIED {
		label := "Reasoning"
		if entry.reasoning == alfredv1.ReasoningTextKind_REASONING_TEXT_KIND_SUMMARY {
			label = "Reasoning summary"
		}
		return lipgloss.NewStyle().Faint(true).Italic(true).Render(
			label + "\n" + lipgloss.Wrap(entry.content, max(width, 1), ""),
		)
	}
	switch entry.role {
	case alfredv1.MessageRole_MESSAGE_ROLE_USER:
		return renderUserMessage(entry.content, width)
	case alfredv1.MessageRole_MESSAGE_ROLE_ASSISTANT:
		return renderMarkdown(renderer, rendererErr, entry.content)
	default:
		return entry.content
	}
}

func renderUserMessage(content string, width int) string {
	content = lipgloss.Wrap(content, max(width-2, 1), "")
	return lipgloss.NewStyle().
		Border(lipgloss.ThickBorder(), false, false, false, true).
		PaddingLeft(1).
		Bold(true).
		Render(content)
}

func renderMarkdown(renderer *glamour.TermRenderer, rendererErr error, content string) string {
	if rendererErr != nil {
		return content
	}
	rendered, err := renderer.Render(content)
	if err != nil {
		return content
	}
	return strings.TrimSpace(rendered)
}

func newMarkdownRenderer(width int) (*glamour.TermRenderer, error) {
	return glamour.NewTermRenderer(
		glamour.WithStyles(markdownStyle()),
		glamour.WithWordWrap(max(width, 1)),
	)
}

func markdownStyle() ansi.StyleConfig {
	style := styles.ASCIIStyleConfig
	style.Document.BlockPrefix = ""
	style.Document.BlockSuffix = ""
	style.Document.Margin = uintPointer(0)
	style.Heading.Bold = boolPointer(true)
	style.H1.Prefix = ""
	style.H2.Prefix = ""
	style.H3.Prefix = ""
	style.H4.Prefix = ""
	style.H5.Prefix = ""
	style.H6.Prefix = ""
	style.Strong.BlockPrefix = ""
	style.Strong.BlockSuffix = ""
	style.Strong.Bold = boolPointer(true)
	style.Emph.BlockPrefix = ""
	style.Emph.BlockSuffix = ""
	style.Emph.Italic = boolPointer(true)
	style.Strikethrough.BlockPrefix = ""
	style.Strikethrough.BlockSuffix = ""
	style.Strikethrough.CrossedOut = boolPointer(true)
	style.Link.Underline = boolPointer(true)
	style.LinkText.Underline = boolPointer(true)
	style.Code.BlockPrefix = ""
	style.Code.BlockSuffix = ""
	style.Code.Inverse = boolPointer(true)
	style.CodeBlock.Margin = uintPointer(0)
	return style
}

func boolPointer(value bool) *bool {
	return &value
}

func uintPointer(value uint) *uint {
	return &value
}

func contentText(parts []*alfredv1.ContentPart) string {
	content := make([]string, 0, len(parts))
	for _, part := range parts {
		switch value := part.GetValue().(type) {
		case *alfredv1.ContentPart_Text:
			content = append(content, value.Text)
		case *alfredv1.ContentPart_Resource:
			name := value.Resource.GetResource().GetName()
			if name == "" {
				name = value.Resource.GetResource().GetUri()
			}
			content = append(content, fmt.Sprintf("[%s]", name))
		}
	}
	return strings.Join(content, "")
}
