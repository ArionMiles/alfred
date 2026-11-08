package tui

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"

	alfredv1 "github.com/ArionMiles/alfred/gen/alfred/v1"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

type sessionItem struct {
	session *alfredv1.Session
	current bool
}

func (item sessionItem) Title() string {
	title := strings.TrimSpace(item.session.GetTitle())
	if title == "" {
		title = "Untitled session"
	}
	if item.current {
		title += " · current"
	}
	return title
}

func (item sessionItem) Description() string {
	id := item.session.GetId()
	return fmt.Sprintf("%s · %s", id[:min(len(id), 12)], item.session.GetCreatedAt().AsTime().Local().Format("Jan 2, 2006 15:04"))
}

func (item sessionItem) FilterValue() string { return item.Title() }

type sessionPicker struct {
	loading bool
	err     error
}

type sessionsListedMsg struct {
	picker   *sessionPicker
	sessions []*alfredv1.Session
	err      error
}

func (model Model) openSessions() (tea.Model, tea.Cmd) {
	if model.project == nil || !model.inspectedSkills {
		return model, nil
	}
	width, height := model.paletteSize()
	palette := newPickerList("Sessions", nil, width, height)
	model.palette = &palette
	model.sessions = &sessionPicker{loading: true}
	model.composer.Blur()
	model.gateInput.Blur()
	return model, model.listSessions(model.sessions)
}

func (model Model) listSessions(picker *sessionPicker) tea.Cmd {
	return func() tea.Msg {
		var sessions []*alfredv1.Session
		var after *alfredv1.SessionCursor
		for {
			response, err := model.clients.Session.ListSessions(model.ctx, &alfredv1.ListSessionsRequest{
				ProjectId: model.project.GetId(), PageSize: 100, After: after,
			})
			if err != nil {
				return sessionsListedMsg{picker: picker, err: err}
			}
			sessions = append(sessions, response.GetSessions()...)
			after = response.GetNext()
			if after == nil {
				break
			}
		}
		sort.Slice(sessions, func(left, right int) bool {
			a, b := sessions[left], sessions[right]
			createdA, createdB := a.GetCreatedAt().AsTime(), b.GetCreatedAt().AsTime()
			if createdA.Equal(createdB) {
				return a.GetId() > b.GetId()
			}
			return createdA.After(createdB)
		})
		return sessionsListedMsg{picker: picker, sessions: sessions}
	}
}

func (model Model) updateSessionsListed(message sessionsListedMsg) (tea.Model, tea.Cmd) {
	if model.sessions != message.picker {
		return model, nil
	}
	model.sessions.loading = false
	model.sessions.err = message.err
	items := make([]list.Item, len(message.sessions))
	for index, session := range message.sessions {
		items[index] = sessionItem{session: session, current: session.GetId() == model.sessionID}
	}
	palette := *model.palette
	command := palette.SetItems(items)
	model.palette = &palette
	return model, command
}

func (model Model) selectSession() (tea.Model, tea.Cmd) {
	const op = "selectSession"

	selected, ok := model.palette.SelectedItem().(sessionItem)
	if !ok {
		return model, nil
	}
	model.palette = nil
	model.sessions = nil
	if selected.session.GetId() == model.sessionID {
		return model, model.focusInput()
	}
	if model.submitting || model.canceling || model.resuming || model.resolvingGate != "" || model.settingTrust {
		model.actionErr = errors.B.Op(op).KindConflict().UserMsg("Wait for the current action to finish before switching sessions.").Build()
		return model, model.focusInput()
	}
	watch := model.replaceSession(selected.session.GetId())
	model.composer.Blur()
	model.resizeTranscript()
	return model, tea.Batch(watch, model.progress.Tick)
}

func (model Model) newSession() (tea.Model, tea.Cmd) {
	const op = "newSession"

	if model.submitting || model.canceling || model.resuming || model.resolvingGate != "" || model.settingTrust {
		model.actionErr = errors.B.Op(op).KindConflict().UserMsg("Wait for the current action to finish before starting a new session.").Build()
		return model, model.focusInput()
	}
	model.resetSession("")
	model.composer.Reset()
	model.resizeTranscript()
	return model, model.focusInput()
}
