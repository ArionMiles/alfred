package tui

import (
	"strings"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	alfredv1 "github.com/ArionMiles/alfred/gen/alfred/v1"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

type setupOption struct{ option *alfredv1.SetupFieldOption }

func (item setupOption) Title() string       { return item.option.GetLabel() }
func (item setupOption) Description() string { return "" }
func (item setupOption) FilterValue() string { return item.Title() }

type setupFieldInput struct {
	definition *alfredv1.SetupField
	input      textinput.Model
	options    list.Model
}

type setupForm struct {
	fields   []setupFieldInput
	focused  int // Fields, followed by Save and Cancel.
	problems []*alfredv1.SetupFieldError
	viewport viewport.Model
}

func newSetupForm(fields []*alfredv1.SetupField, values map[string]string) (*setupForm, error) {
	const op = "newSetupForm"

	result := &setupForm{viewport: viewport.New()}
	for _, definition := range fields {
		field := setupFieldInput{definition: definition, input: textinput.New()}
		field.input.Prompt = "> "
		switch definition.GetKind() {
		case alfredv1.SetupFieldKind_SETUP_FIELD_KIND_SECRET:
			field.input.EchoMode = textinput.EchoPassword
		case alfredv1.SetupFieldKind_SETUP_FIELD_KIND_TEXT:
			field.input.SetValue(values[definition.GetId()])
		case alfredv1.SetupFieldKind_SETUP_FIELD_KIND_SELECT:
			field.options = newSetupOptions(definition.GetOptions(), values[definition.GetId()])
		default:
			return nil, errors.B.Op(op).KindUnimplemented().UserMsg("This setup form contains an unsupported input type.").Build()
		}
		result.fields = append(result.fields, field)
	}
	return result, nil
}

func newSetupOptions(options []*alfredv1.SetupFieldOption, value string) list.Model {
	items := make([]list.Item, len(options))
	selected := 0
	for index, option := range options {
		items[index] = setupOption{option: option}
		if option.GetValue() == value {
			selected = index
		}
	}
	delegate := list.NewDefaultDelegate()
	delegate.ShowDescription = false
	delegate.SetSpacing(0)
	choices := list.New(items, delegate, 40, 3)
	choices.SetFilteringEnabled(false)
	choices.SetShowTitle(false)
	choices.SetShowStatusBar(false)
	choices.SetShowHelp(false)
	choices.SetShowPagination(false)
	choices.DisableQuitKeybindings()
	choices.Select(selected)
	return choices
}

func (form *setupForm) focus(index int) tea.Cmd {
	form.focused = (index + len(form.fields) + 2) % (len(form.fields) + 2)
	for index := range form.fields {
		form.fields[index].input.Blur()
	}
	var command tea.Cmd
	if form.focused < len(form.fields) && form.fields[form.focused].definition.GetKind() != alfredv1.SetupFieldKind_SETUP_FIELD_KIND_SELECT {
		command = form.fields[form.focused].input.Focus()
	}
	form.refresh(true)
	return command
}

func (form *setupForm) values() map[string]string {
	values := make(map[string]string, len(form.fields))
	for index := range form.fields {
		field := &form.fields[index]
		value := field.input.Value()
		if field.definition.GetKind() == alfredv1.SetupFieldKind_SETUP_FIELD_KIND_SELECT {
			if selected, ok := field.options.SelectedItem().(setupOption); ok {
				value = selected.option.GetValue()
			}
		}
		values[field.definition.GetId()] = value
	}
	return values
}

type setupFormAction uint8

const (
	formContinue setupFormAction = iota
	formSave
	formCancel
)

func (form *setupForm) updateKey(message tea.KeyPressMsg) (tea.Cmd, setupFormAction) {
	switch message.Code {
	case tea.KeyTab:
		if message.Mod == tea.ModShift {
			return form.focus(form.focused - 1), formContinue
		}
		return form.focus(form.focused + 1), formContinue
	case tea.KeyEnter:
		switch form.focused {
		case len(form.fields):
			return nil, formSave
		case len(form.fields) + 1:
			return nil, formCancel
		default:
			return form.focus(form.focused + 1), formContinue
		}
	}
	return form.update(message), formContinue
}

func (form *setupForm) update(message tea.Msg) tea.Cmd {
	var command tea.Cmd
	if form.focused < len(form.fields) {
		field := &form.fields[form.focused]
		if field.definition.GetKind() == alfredv1.SetupFieldKind_SETUP_FIELD_KIND_SELECT {
			field.options, command = field.options.Update(message)
		} else {
			field.input, command = field.input.Update(message)
		}
	}
	form.refresh(false)
	if _, keyPress := message.(tea.KeyPressMsg); keyPress {
		return command
	}
	var scroll tea.Cmd
	form.viewport, scroll = form.viewport.Update(message)
	return tea.Batch(command, scroll)
}

func (form *setupForm) resize(width, height int) {
	form.viewport.SetWidth(width)
	form.viewport.SetHeight(max(height-11, 3))
	for index := range form.fields {
		form.fields[index].input.SetWidth(max(width-4, 1))
		if form.fields[index].definition.GetKind() == alfredv1.SetupFieldKind_SETUP_FIELD_KIND_SELECT {
			form.fields[index].options.SetWidth(width)
		}
	}
	form.refresh(true)
}

func (form *setupForm) refresh(followFocus bool) {
	var blocks []string
	focusStart, focusEnd, lines := 0, 0, 0
	for index := range form.fields {
		field := &form.fields[index]
		block := field.view(form.problems)
		block = lipgloss.NewStyle().Width(form.viewport.Width()).Render(block) + "\n"
		if index == form.focused {
			focusStart, focusEnd = lines, lines+lipgloss.Height(block)
		}
		lines += lipgloss.Height(block)
		blocks = append(blocks, block)
	}
	form.viewport.SetContent(strings.Join(blocks, "\n"))
	if followFocus && form.focused < len(form.fields) {
		if focusStart < form.viewport.YOffset() {
			form.viewport.SetYOffset(focusStart)
		} else if focusEnd > form.viewport.YOffset()+form.viewport.Height() {
			form.viewport.SetYOffset(max(focusEnd-form.viewport.Height(), focusStart))
		}
	}
}

func (field *setupFieldInput) view(problems []*alfredv1.SetupFieldError) string {
	label := field.definition.GetLabel()
	if field.definition.GetRequired() {
		label += " *"
	}
	lines := []string{label}
	if description := field.definition.GetDescription(); description != "" {
		lines = append(lines, description)
	}
	if field.definition.GetKind() == alfredv1.SetupFieldKind_SETUP_FIELD_KIND_SELECT {
		lines = append(lines, field.options.View())
	} else {
		lines = append(lines, field.input.View())
	}
	for _, problem := range problems {
		if problem.GetFieldId() == field.definition.GetId() {
			lines = append(lines, "! "+problem.GetMessage())
		}
	}
	return strings.Join(lines, "\n")
}

func (form *setupForm) view() string {
	save, cancel := "[ Save ]", "[ Cancel ]"
	if form.focused == len(form.fields) {
		save = "> " + save
	}
	if form.focused == len(form.fields)+1 {
		cancel = "> " + cancel
	}
	return form.viewport.View() + "\n" + save + "  " + cancel
}

func userErrorMessage(err error) string {
	if message := errors.UserMsg(err); message != "" {
		return message
	}
	return err.Error()
}
