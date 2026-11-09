// Package tui implements Alfred's terminal client.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	alfredv1 "github.com/ArionMiles/alfred/gen/alfred/v1"
	"github.com/ArionMiles/alfred/gen/alfred/v1/alfredv1connect"
	"github.com/ArionMiles/alfred/pkg/transport/rpc"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

// Model is Alfred's Bubble Tea application state.
type Model struct {
	ctx             context.Context
	openURL         func(context.Context, string) error
	clients         rpc.Clients
	root            string
	project         *alfredv1.Project
	projectSkills   *alfredv1.InspectProjectSkillsResponse
	activeRun       *alfredv1.Run
	sessionID       string
	lastSequence    uint64
	replaying       bool
	composer        composer
	gateInput       textinput.Model
	gates           pendingGates
	transcript      transcript
	progress        spinner.Model
	watch           *sessionWatch
	watchGeneration uint64
	watchState      sessionWatchState
	watchErr        error
	palette         *list.Model
	sessions        *sessionPicker
	providers       *providerSetup
	models          *modelPicker
	searchSetup     *searchSetup
	keys            keyMap
	inspectedSkills bool
	settingTrust    bool
	submitting      bool
	canceling       bool
	resuming        bool
	resolvingGate   string
	height          int
	err             error
	actionErr       error
}

type sessionWatch struct {
	stream     alfredv1connect.SessionServiceWatchSessionClientStream
	cancel     context.CancelFunc
	generation uint64
}

type sessionWatchState uint8

const (
	sessionWatchStopped sessionWatchState = iota
	sessionWatchConnecting
	sessionWatchConnected
	sessionWatchDisconnected
	sessionWatchReconnecting
)

type keyMap struct {
	submit    key.Binding
	trust     key.Binding
	deny      key.Binding
	interrupt key.Binding
	palette   key.Binding
}

type projectResolvedMsg struct {
	project *alfredv1.Project
	err     error
}

type projectSkillsInspectedMsg struct {
	state *alfredv1.InspectProjectSkillsResponse
	err   error
}

type projectSkillTrustSetMsg struct {
	trust alfredv1.ProjectSkillTrust
	err   error
}

type invocationCompletedMsg struct {
	run *alfredv1.Run
	err error
}

type runCanceledMsg struct {
	runID string
	err   error
}

type gateResolvedMsg struct {
	key string
	err error
}

type sessionActivityMsg struct {
	sessionID  string
	generation uint64
	watch      *sessionWatch
	response   *alfredv1.WatchSessionResponse
	err        error
}

type sessionReconnectMsg struct {
	sessionID  string
	generation uint64
}

// NewModel creates a TUI that begins by resolving root through the service.
func NewModel(ctx context.Context, config Config, clients rpc.Clients, root string) Model {
	return Model{
		ctx:        ctx,
		openURL:    openBrowserURL,
		clients:    clients,
		root:       root,
		composer:   newComposer(commands),
		gateInput:  newGateInput(),
		transcript: newTranscript(),
		progress:   spinner.New(spinner.WithSpinner(spinner.Dot)),
		keys: keyMap{
			submit:    key.NewBinding(key.WithKeys(config.Keybindings.Submit...)),
			trust:     key.NewBinding(key.WithKeys("t", "y")),
			deny:      key.NewBinding(key.WithKeys("d", "n")),
			interrupt: key.NewBinding(key.WithKeys(config.Keybindings.Interrupt...)),
			palette:   key.NewBinding(key.WithKeys(config.Keybindings.Palette...)),
		},
	}
}

// Init resolves the working directory to Alfred's durable project.
func (model Model) Init() tea.Cmd {
	return func() tea.Msg {
		response, err := model.clients.Project.ResolveProject(model.ctx, &alfredv1.ResolveProjectRequest{
			Root: model.root,
		})
		if err != nil {
			return projectResolvedMsg{err: err}
		}
		return projectResolvedMsg{project: response.GetProject()}
	}
}

// Update applies RPC results to the TUI state.
func (model Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case projectResolvedMsg:
		model.project = message.project
		model.err = message.err
		if message.err == nil {
			return model, model.inspectProjectSkills()
		}
	case projectSkillsInspectedMsg:
		model.projectSkills = message.state
		model.inspectedSkills = true
		model.err = message.err
		if message.err == nil && !model.requiresSkillTrust() {
			return model, model.focusInput()
		}
	case projectSkillTrustSetMsg:
		model.settingTrust = false
		model.actionErr = message.err
		if message.err == nil {
			model.project.SkillTrust = message.trust
			model.projectSkills.Trust = message.trust
		}
		return model, model.focusInput()
	case invocationCompletedMsg:
		return model.updateInvocationCompleted(message)
	case runCanceledMsg:
		return model.updateRunCanceled(message)
	case runResumedMsg:
		return model.updateRunResumed(message)
	case gateResolvedMsg:
		return model.updateGateResolved(message)
	case sessionActivityMsg:
		return model.updateSessionActivity(message)
	case sessionReconnectMsg:
		return model.updateSessionReconnect(message)
	case sessionsListedMsg:
		return model.updateSessionsListed(message)
	case modelsListedMsg:
		return model.updateModelsListed(message)
	case modelSelectedMsg:
		return model.updateModelSelected(message)
	case providerDefinitionsMsg:
		return model.updateProviderDefinitions(message)
	case searchBackendsMsg:
		return model.updateSearchBackends(message)
	case searchFormMsg:
		return model.updateSearchForm(message)
	case searchSavedMsg:
		return model.updateSearchSaved(message)
	case providerFormMsg:
		return model.updateProviderFormLoaded(message)
	case providerSavedMsg:
		return model.updateProviderSaved(message)
	case providerAuthorizationStartedMsg:
		return model.updateProviderAuthorizationStarted(message)
	case providerAuthorizationPollMsg:
		return model.updateProviderAuthorizationPoll(message)
	case providerAuthorizationCanceledMsg:
		return model.updateProviderAuthorizationCanceled(message)
	case tea.WindowSizeMsg:
		return model.updateWindowSize(message)
	case spinner.TickMsg:
		return model.updateProgress(message)
	case tea.KeyPressMsg:
		return model.updateKeyPress(message)
	}

	if model.palette != nil {
		return model.updatePaletteActivity(message)
	}
	var command tea.Cmd
	if gate, ok := model.gates.current(); ok {
		if gate.kind == inputGate {
			model.gateInput, command = model.gateInput.Update(message)
		}
	} else {
		model.composer, command = model.composer.Update(message)
	}
	var transcriptCommand tea.Cmd
	model.transcript, transcriptCommand = model.transcript.Update(message)
	return model, tea.Batch(command, transcriptCommand)
}

func (model Model) updateWindowSize(message tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	model.height = message.Height
	model.composer.SetWidth(max(message.Width-2, 1))
	model.gateInput.SetWidth(max(message.Width-4, 1))
	model.resizeTranscript()
	if model.palette != nil {
		palette := *model.palette
		width, height := model.paletteSize()
		palette.SetSize(width, height)
		model.palette = &palette
	}
	if model.providers != nil && model.providers.form != nil {
		model.resizeProviderForm()
	}
	if model.searchSetup != nil && model.searchSetup.form != nil {
		width, _ := model.paletteSize()
		model.searchSetup.form.resize(width, model.height)
	}
	return model, nil
}

func (model Model) updateInvocationCompleted(message invocationCompletedMsg) (tea.Model, tea.Cmd) {
	model.submitting = false
	model.actionErr = message.err
	if message.err == nil {
		newSessionID := message.run.GetSessionId()
		model.composer.Reset()
		if newSessionID != model.sessionID {
			watch := model.replaceSession(newSessionID)
			model.activeRun = message.run
			model.resizeTranscript()
			return model, tea.Batch(model.focusInput(), watch)
		}
		model.activeRun = message.run
	}
	model.resizeTranscript()
	return model, model.focusInput()
}

func (model Model) updateRunCanceled(message runCanceledMsg) (tea.Model, tea.Cmd) {
	if model.activeRun == nil || model.activeRun.GetId() != message.runID {
		return model, nil
	}
	model.canceling = false
	model.actionErr = message.err
	if message.err == nil {
		model.activeRun.Status = alfredv1.RunStatus_RUN_STATUS_CANCELLED
		model.transcript.discardProvisional()
		previousGate := model.currentGateKey()
		model.gates.remove("", message.runID)
		focus := model.syncGateFocus(previousGate)
		model.resizeTranscript()
		return model, focus
	}
	model.resizeTranscript()
	return model, nil
}

func (model Model) updateGateResolved(message gateResolvedMsg) (tea.Model, tea.Cmd) {
	if !model.gates.contains(message.key) {
		if model.resolvingGate == message.key {
			model.resolvingGate = ""
		}
		return model, nil
	}
	model.resolvingGate = ""
	model.actionErr = message.err
	if message.err != nil {
		model.resizeTranscript()
		return model, nil
	}
	previousGate := model.currentGateKey()
	model.gates.remove(message.key, "")
	focus := model.syncGateFocus(previousGate)
	model.resizeTranscript()
	return model, focus
}

func (model Model) updateSessionReconnect(message sessionReconnectMsg) (tea.Model, tea.Cmd) {
	if message.sessionID != model.sessionID || message.generation != model.watchGeneration ||
		model.watch != nil {
		return model, nil
	}
	model.watchState = sessionWatchReconnecting
	return model, model.watchSession(message.sessionID, model.lastSequence, message.generation)
}

func (model Model) updateProgress(message spinner.TickMsg) (tea.Model, tea.Cmd) {
	if !model.showsProgressSpinner() {
		return model, nil
	}
	var command tea.Cmd
	model.progress, command = model.progress.Update(message)
	return model, command
}

func (model Model) updateKeyPress(message tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if key.Matches(message, model.keys.interrupt) {
		if model.canCancelActiveRun() {
			model.canceling = true
			model.actionErr = nil
			model.resizeTranscript()
			return model, tea.Batch(model.cancelActiveRun(), model.progress.Tick)
		}
		return model, nil
	}
	if key.Matches(message, model.keys.palette) {
		return model.togglePalette()
	}
	if model.palette != nil {
		return model.updatePaletteKey(message)
	}
	if gate, ok := model.gates.current(); ok {
		return model.updateGateKey(message, gate)
	}
	if model.settingTrust {
		return model, nil
	}
	if model.requiresSkillTrust() {
		switch {
		case key.Matches(message, model.keys.trust):
			model.settingTrust = true
			return model, model.setProjectSkillTrust(
				alfredv1.ProjectSkillTrust_PROJECT_SKILL_TRUST_TRUSTED,
			)
		case key.Matches(message, model.keys.deny):
			model.settingTrust = true
			return model, model.setProjectSkillTrust(
				alfredv1.ProjectSkillTrust_PROJECT_SKILL_TRUST_DENIED,
			)
		}
		return model, nil
	}
	if key.Matches(message, model.keys.submit) {
		if selected, ok := model.composer.commandForEnter(); ok {
			model.composer.Reset()
			return model.executeCommand(selected)
		}
		if model.composer.hasSuggestions() {
			model.composer.acceptSuggestion()
			return model, nil
		}
		input := strings.TrimSpace(model.composer.Value())
		if input != "" && model.canSubmit() {
			model.submitting = true
			model.actionErr = nil
			model.resizeTranscript()
			return model, tea.Batch(model.invoke(input), model.progress.Tick)
		}
	}

	var command tea.Cmd
	model.composer, command = model.composer.Update(message)
	var transcriptCommand tea.Cmd
	model.transcript, transcriptCommand = model.transcript.Update(message)
	return model, tea.Batch(command, transcriptCommand)
}

// View renders the current project-resolution state.
func (model Model) View() tea.View {
	if model.err != nil {
		return model.terminalView(fmt.Sprintf("Alfred\n\nUnable to open this project: %v", model.err))
	}
	if model.project == nil {
		return model.terminalView("Alfred\n\nResolving project…")
	}
	if !model.inspectedSkills {
		return model.terminalView("Alfred\n\nChecking project skills…")
	}
	header := fmt.Sprintf(
		"Alfred\n\nProject: %s\nRoot: %s",
		model.project.GetName(),
		model.project.GetRoot(),
	)
	if model.requiresSkillTrust() {
		status := "[t] Trust  [d] Deny"
		if model.settingTrust {
			status = "Saving trust decision…"
		}
		if model.actionErr != nil {
			status = fmt.Sprintf("Could not save trust decision: %v\n%s", model.actionErr, status)
		}
		return model.terminalView(fmt.Sprintf(
			"%s\n\nProject skills were found:\n- %s\n\nAllow this project to load them?\n%s",
			header,
			strings.Join(model.projectSkills.GetRoots(), "\n- "),
			status,
		))
	}
	status := strings.ToLower(strings.TrimPrefix(model.project.GetSkillTrust().String(), "PROJECT_SKILL_TRUST_"))
	content := model.readyContent(header, status)
	return model.terminalView(model.conversationView(content))
}

func (model Model) terminalView(content string) tea.View {
	if model.palette != nil {
		content = model.overlayPalette(content)
	}
	view := tea.NewView(content)
	view.AltScreen = true
	view.MouseMode = tea.MouseModeCellMotion
	view.ProgramStatus = model.programStatus()
	return view
}

func (model Model) programStatus() *tea.ProgramStatus {
	status := &tea.ProgramStatus{App: "alfred", Title: "Alfred"}
	switch {
	case model.err != nil:
		status.State = tea.ProgramStateError
		status.Message = "Could not open the project"
	case model.project == nil:
		status.State = tea.ProgramStateWorking
		status.Message = "Opening the project"
	case !model.inspectedSkills:
		status.State = tea.ProgramStateWorking
		status.Message = "Checking project skills"
	case model.requiresSkillTrust():
		status.State = tea.ProgramStateBlocked
		status.Kind = tea.ProgramStatusKindPermission
		status.Message = "Waiting for project skill trust"
	case model.models != nil && model.models.saving:
		status.State = tea.ProgramStateWorking
		status.Message = "Saving model selection"
	case model.providers != nil && model.providers.saving:
		status.State = tea.ProgramStateWorking
		status.Message = "Saving provider settings"
	case model.searchSetup != nil && model.searchSetup.saving:
		status.State = tea.ProgramStateWorking
		status.Message = "Saving search settings"
	case model.replaying:
		status.State = tea.ProgramStateWorking
		status.Message = "Loading session history"
	case model.submitting:
		status.State = tea.ProgramStateWorking
		status.Message = "Submitting a message"
	case model.canceling:
		status.State = tea.ProgramStateWorking
		status.Message = "Canceling run"
	case model.resuming:
		status.State = tea.ProgramStateWorking
		status.Message = "Resuming run"
	case model.resolvingGate != "":
		status.State = tea.ProgramStateWorking
		status.Message = "Responding to run request"
	case model.hasGateKind(approvalGate):
		status.State = tea.ProgramStateBlocked
		status.Kind = tea.ProgramStatusKindPermission
		status.Message = "Waiting for approval"
	case model.hasGateKind(inputGate):
		status.State = tea.ProgramStateBlocked
		status.Kind = tea.ProgramStatusKindQuestion
		status.Message = "Waiting for input"
	case model.activeRun != nil:
		setRunProgramStatus(status, model.activeRun.GetStatus())
	case model.watchState == sessionWatchDisconnected || model.watchState == sessionWatchReconnecting:
		status.State = tea.ProgramStateWorking
		status.Message = "Reconnecting the session"
	default:
		status.State = tea.ProgramStateIdle
		status.Message = "Ready"
	}
	return status
}

func setRunProgramStatus(status *tea.ProgramStatus, runStatus alfredv1.RunStatus) {
	switch runStatus {
	case alfredv1.RunStatus_RUN_STATUS_QUEUED:
		status.State = tea.ProgramStateWorking
		status.Message = "Run queued"
	case alfredv1.RunStatus_RUN_STATUS_RUNNING:
		status.State = tea.ProgramStateWorking
		status.Message = "Run in progress"
	case alfredv1.RunStatus_RUN_STATUS_WAITING_FOR_APPROVAL:
		status.State = tea.ProgramStateBlocked
		status.Kind = tea.ProgramStatusKindPermission
		status.Message = "Waiting for approval"
	case alfredv1.RunStatus_RUN_STATUS_WAITING_FOR_INPUT:
		status.State = tea.ProgramStateBlocked
		status.Kind = tea.ProgramStatusKindQuestion
		status.Message = "Waiting for input"
	case alfredv1.RunStatus_RUN_STATUS_INTERRUPTED:
		status.State = tea.ProgramStateBlocked
		status.Message = "Run interrupted"
	case alfredv1.RunStatus_RUN_STATUS_COMPLETED:
		status.State = tea.ProgramStateDone
		status.Message = "Run completed"
	case alfredv1.RunStatus_RUN_STATUS_FAILED:
		status.State = tea.ProgramStateError
		status.Message = "Run failed"
	case alfredv1.RunStatus_RUN_STATUS_CANCELLED:
		status.State = tea.ProgramStateDone
		status.Message = "Run cancelled"
	default:
		status.State = tea.ProgramStateIdle
		status.Message = "Ready"
	}
}

func (model Model) readyContent(header, skillStatus string) string {
	content := fmt.Sprintf("%s\nProject skills: %s", header, skillStatus)
	if model.sessionID != "" {
		content = fmt.Sprintf(
			"%s\nSession: %s\nSession watch: %s",
			content,
			model.sessionID,
			model.watchStatus(),
		)
	}
	if model.activeRun != nil {
		runStatus := strings.ToLower(strings.TrimPrefix(model.activeRun.GetStatus().String(), "RUN_STATUS_"))
		content = fmt.Sprintf("%s\n\nRun: %s", content, runStatus)
	}
	if model.actionErr != nil {
		content = fmt.Sprintf("%s\n\nCould not complete action: %v", content, model.actionErr)
	}
	return content
}

func (model Model) watchStatus() string {
	switch model.watchState {
	case sessionWatchConnecting:
		return "connecting"
	case sessionWatchConnected:
		if model.replaying {
			return "loading history"
		}
		return "connected"
	case sessionWatchDisconnected:
		if model.watchErr != nil {
			return fmt.Sprintf("disconnected (%v); retrying…", model.watchErr)
		}
		return "disconnected; retrying…"
	case sessionWatchReconnecting:
		return "reconnecting"
	default:
		return "stopped"
	}
}

func (model Model) conversationView(content string) string {
	menu := model.composer.suggestionView()
	if _, ok := model.gates.current(); ok {
		menu = ""
	}
	progress := model.progressView()
	input := model.inputView()
	inputLines := strings.Split(input, "\n")
	if model.height == 0 {
		transcriptView := strings.TrimSpace(model.transcript.View(72, 1))
		if transcriptView != "" {
			content = fmt.Sprintf("%s\n\n%s", content, transcriptView)
		}
		if menu != "" {
			content = fmt.Sprintf("%s\n\n%s", content, menu)
		}
		if progress != "" {
			content = fmt.Sprintf("%s\n\n%s", content, progress)
		}
		return fmt.Sprintf("%s\n%s", content, input)
	}

	contentLines := strings.Split(content, "\n")
	footerHeight := len(inputLines)
	if progress != "" {
		footerHeight++
	}
	transcriptHeight := max(model.height-len(contentLines)-footerHeight-1, 1)
	transcriptLines := strings.Split(model.transcript.View(model.composer.input.Width(), transcriptHeight), "\n")
	lines := make([]string, 0, model.height)
	lines = append(lines, contentLines...)
	lines = append(lines, "")
	lines = append(lines, transcriptLines...)
	bodyHeight := max(model.height-footerHeight, 0)
	if len(lines) >= bodyHeight {
		lines = lines[:bodyHeight]
	} else {
		lines = append(lines, make([]string, bodyHeight-len(lines))...)
	}
	if progress != "" {
		lines = append(lines, progress)
	}
	lines = append(lines, inputLines...)
	if menu == "" {
		return strings.Join(lines, "\n")
	}

	menuLines := strings.Split(menu, "\n")
	start := max(len(lines)-len(menuLines)-1, 0)
	copy(lines[start:len(lines)-1], menuLines[max(len(menuLines)-(len(lines)-1), 0):])
	return strings.Join(lines, "\n")
}

func (model Model) inspectProjectSkills() tea.Cmd {
	return func() tea.Msg {
		response, err := model.clients.Project.InspectProjectSkills(
			model.ctx,
			&alfredv1.InspectProjectSkillsRequest{ProjectId: model.project.GetId()},
		)
		return projectSkillsInspectedMsg{state: response, err: err}
	}
}

func (model Model) invoke(input string) tea.Cmd {
	return func() tea.Msg {
		response, err := model.clients.Invocation.Invoke(model.ctx, &alfredv1.InvokeRequest{
			ProjectId: model.project.GetId(),
			SessionId: model.sessionID,
			Source:    alfredv1.InvocationSource_INVOCATION_SOURCE_USER,
			Input: []*alfredv1.ContentPart{{
				Value: &alfredv1.ContentPart_Text{Text: input},
			}},
		})
		if err != nil {
			return invocationCompletedMsg{err: err}
		}
		return invocationCompletedMsg{run: response.GetRun()}
	}
}

func (model Model) cancelActiveRun() tea.Cmd {
	runID := model.activeRun.GetId()
	return func() tea.Msg {
		_, err := model.clients.Run.CancelRun(model.ctx, &alfredv1.CancelRunRequest{RunId: runID})
		return runCanceledMsg{runID: runID, err: err}
	}
}

func (model Model) resolveApproval(gate interactionGate, outcome alfredv1.ApprovalOutcome) tea.Cmd {
	gateKey := gate.key()
	return func() tea.Msg {
		_, err := model.clients.Run.ResolveApproval(model.ctx, &alfredv1.ResolveApprovalRequest{
			RunId: gate.runID, ApprovalId: gate.approval.GetId(), Outcome: outcome,
		})
		return gateResolvedMsg{key: gateKey, err: err}
	}
}

func (model Model) provideInput(gate interactionGate, input string) tea.Cmd {
	gateKey := gate.key()
	return func() tea.Msg {
		_, err := model.clients.Run.ProvideInput(model.ctx, &alfredv1.ProvideInputRequest{
			RunId:     gate.runID,
			RequestId: gate.input.GetId(),
			Content: []*alfredv1.ContentPart{{
				Value: &alfredv1.ContentPart_Text{Text: input},
			}},
		})
		return gateResolvedMsg{key: gateKey, err: err}
	}
}

func (model Model) watchSession(sessionID string, afterSequence, generation uint64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithCancel(model.ctx)
		stream, err := model.clients.Session.WatchSession(ctx, &alfredv1.WatchSessionRequest{
			SessionId:     sessionID,
			AfterSequence: afterSequence,
		})
		if err != nil {
			cancel()
			return sessionActivityMsg{sessionID: sessionID, generation: generation, err: err}
		}
		watch := &sessionWatch{stream: stream, cancel: cancel, generation: generation}
		response, err := stream.Receive()
		return sessionActivityMsg{
			sessionID:  sessionID,
			generation: generation,
			watch:      watch,
			response:   response,
			err:        err,
		}
	}
}

func receiveSessionActivity(sessionID string, watch *sessionWatch) tea.Cmd {
	return func() tea.Msg {
		response, err := watch.stream.Receive()
		return sessionActivityMsg{
			sessionID:  sessionID,
			generation: watch.generation,
			watch:      watch,
			response:   response,
			err:        err,
		}
	}
}

func (model Model) updateSessionActivity(message sessionActivityMsg) (tea.Model, tea.Cmd) {
	if message.sessionID != model.sessionID || message.generation != model.watchGeneration {
		if message.watch != nil {
			message.watch.cancel()
		}
		return model, nil
	}
	if model.watch == nil {
		model.watch = message.watch
	} else if message.watch != model.watch {
		if message.watch != nil {
			message.watch.cancel()
		}
		return model, nil
	}
	if message.err != nil {
		model.watchErr = message.err
		model.replaying = true
		model.watchState = sessionWatchDisconnected
		model.closeWatch()
		model.transcript.markDisconnected()
		model.resizeTranscript()
		return model, reconnectSessionAfter(model.sessionID, model.watchGeneration)
	}

	wasReplaying := model.replaying
	previousGate := model.currentGateKey()
	model.applySessionActivity(message.response)
	focus := model.syncGateFocus(previousGate)
	if wasReplaying && !model.replaying {
		focus = model.focusInput()
	}
	model.watchState = sessionWatchConnected
	model.watchErr = nil
	model.resizeTranscript()
	return model, tea.Batch(receiveSessionActivity(model.sessionID, model.watch), focus)
}

func (model *Model) applySessionActivity(response *alfredv1.WatchSessionResponse) {
	if event := response.GetEvent(); event != nil {
		if event.GetSequence() <= model.lastSequence {
			return
		}
		model.lastSequence = event.GetSequence()
		model.gates.apply(event)
		model.applyRunActivity(event)
		model.applyTranscriptEvent(event)
		return
	}
	if caughtUp := response.GetCaughtUp(); caughtUp != nil {
		model.lastSequence = max(model.lastSequence, caughtUp.GetThroughSequence())
		model.replaying = false
		return
	}
	if update := response.GetUpdate(); update != nil {
		if model.activeRun != nil && model.activeRun.GetId() == update.GetRunId() && runIsTerminal(model.activeRun.GetStatus()) {
			return
		}
		model.transcript.addUpdate(update)
		return
	}
	if response.GetUpdateGap() != nil {
		model.transcript.markGap()
	}
}

func (model *Model) applyTranscriptEvent(event *alfredv1.RunEvent) {
	if added := event.GetMessageAdded(); added != nil {
		model.transcript.addMessage(added.GetMessage())
	}
	if response := event.GetInputReceived().GetResponse(); response != nil {
		model.transcript.addMessage(&alfredv1.Message{Role: alfredv1.MessageRole_MESSAGE_ROLE_USER, Content: response.GetContent()})
	}
	if output := event.GetModelOutputCompleted(); output != nil {
		model.transcript.addModelOutput(output)
	}
	if call := event.GetToolCallRequested().GetCall(); call != nil {
		model.transcript.addToolActivity("Tool: " + call.GetToolId() + "\n" + string(call.GetArgumentsJson()))
	}
	if result := event.GetToolCallCompleted().GetResult(); result != nil {
		label := "Tool result"
		if result.GetIsError() {
			label = "Tool error"
		}
		model.transcript.addToolActivity(label + "\n" + contentText(result.GetOutput()))
	}
}

func (model *Model) applyRunActivity(event *alfredv1.RunEvent) {
	if added := event.GetMessageAdded(); added != nil &&
		added.GetMessage().GetRole() == alfredv1.MessageRole_MESSAGE_ROLE_USER &&
		(model.activeRun == nil || model.activeRun.GetId() != event.GetRunId()) {
		// The invocation records its user message after creating the queued run.
		model.activeRun = &alfredv1.Run{
			Id: event.GetRunId(), SessionId: model.sessionID, Status: alfredv1.RunStatus_RUN_STATUS_QUEUED,
		}
	}
	if changed := event.GetRunStateChanged(); changed != nil {
		if model.activeRun == nil {
			model.activeRun = &alfredv1.Run{Id: event.GetRunId(), SessionId: model.sessionID}
		}
		if event.GetRunId() == model.activeRun.GetId() {
			model.activeRun.Status = changed.GetStatus()
			if runIsTerminal(changed.GetStatus()) {
				model.canceling = false
				model.transcript.discardProvisional()
			}
		}
	}
}

func (model *Model) stopWatching() {
	model.closeWatch()
	model.watchGeneration++
	model.watchState = sessionWatchStopped
	model.watchErr = nil
}

func (model *Model) closeWatch() {
	if model.watch != nil {
		model.watch.cancel()
		model.watch = nil
	}
}

func (model *Model) replaceSession(sessionID string) tea.Cmd {
	model.resetSession(sessionID)
	model.replaying = true
	model.watchState = sessionWatchConnecting
	return model.watchSession(sessionID, model.lastSequence, model.watchGeneration)
}

func (model *Model) resetSession(sessionID string) {
	model.stopWatching()
	model.sessionID = sessionID
	model.lastSequence = 0
	model.replaying = false
	model.activeRun = nil
	model.actionErr = nil
	model.canceling = false
	model.transcript = newTranscript()
	model.gates = pendingGates{}
	model.gateInput.Reset()
	model.gateInput.Blur()
	model.resolvingGate = ""
}

func reconnectSessionAfter(sessionID string, generation uint64) tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg {
		return sessionReconnectMsg{sessionID: sessionID, generation: generation}
	})
}

func (model *Model) resizeTranscript() {
	if model.height == 0 {
		return
	}
	contentLines := 1
	if model.project != nil && model.inspectedSkills && !model.requiresSkillTrust() {
		status := strings.ToLower(strings.TrimPrefix(model.project.GetSkillTrust().String(), "PROJECT_SKILL_TRUST_"))
		header := fmt.Sprintf(
			"Alfred\n\nProject: %s\nRoot: %s",
			model.project.GetName(),
			model.project.GetRoot(),
		)
		contentLines = len(strings.Split(model.readyContent(header, status), "\n"))
	}
	footerHeight := len(strings.Split(model.inputView(), "\n"))
	if model.progressView() != "" {
		footerHeight++
	}
	model.transcript.setSize(
		model.composer.input.Width(),
		max(model.height-contentLines-footerHeight-1, 1),
	)
}

func (model Model) canCancelActiveRun() bool {
	return !model.replaying && !model.resuming && model.activeRun != nil && !model.canceling &&
		runIsCancellable(model.activeRun.GetStatus())
}

func (model Model) canSubmit() bool {
	return !model.replaying && !model.submitting && !model.canceling && !model.resuming && model.project != nil &&
		model.currentGateKey() == "" &&
		(model.activeRun == nil || !runIsCancellable(model.activeRun.GetStatus()))
}

func runIsCancellable(status alfredv1.RunStatus) bool {
	switch status {
	case alfredv1.RunStatus_RUN_STATUS_QUEUED,
		alfredv1.RunStatus_RUN_STATUS_RUNNING,
		alfredv1.RunStatus_RUN_STATUS_WAITING_FOR_APPROVAL,
		alfredv1.RunStatus_RUN_STATUS_WAITING_FOR_INPUT,
		alfredv1.RunStatus_RUN_STATUS_INTERRUPTED:
		return true
	default:
		return false
	}
}

func runIsTerminal(status alfredv1.RunStatus) bool {
	return status == alfredv1.RunStatus_RUN_STATUS_COMPLETED ||
		status == alfredv1.RunStatus_RUN_STATUS_FAILED ||
		status == alfredv1.RunStatus_RUN_STATUS_CANCELLED
}

func (model Model) showsProgressSpinner() bool {
	if model.replaying || model.submitting || model.canceling || model.resuming {
		return true
	}
	if model.activeRun == nil {
		return false
	}
	status := model.activeRun.GetStatus()
	return status == alfredv1.RunStatus_RUN_STATUS_QUEUED ||
		status == alfredv1.RunStatus_RUN_STATUS_RUNNING
}

func (model Model) progressView() string {
	if !model.showsProgressSpinner() {
		return ""
	}
	label := "Sending message…"
	switch {
	case model.replaying:
		label = "Loading session history…"
	case model.canceling:
		label = "Canceling run…"
	case model.resuming:
		label = "Resuming run…"
	case model.activeRun != nil && model.activeRun.GetStatus() == alfredv1.RunStatus_RUN_STATUS_QUEUED:
		label = fmt.Sprintf("Queued · %s to cancel", strings.Join(model.keys.interrupt.Keys(), "/"))
	case model.activeRun != nil && model.activeRun.GetStatus() == alfredv1.RunStatus_RUN_STATUS_RUNNING:
		label = fmt.Sprintf("Working… · %s to cancel", strings.Join(model.keys.interrupt.Keys(), "/"))
	}
	return model.progress.View() + label
}

func (model Model) updateGateKey(message tea.KeyPressMsg, gate interactionGate) (tea.Model, tea.Cmd) {
	if model.replaying || model.resuming || model.resolvingGate != "" {
		return model, nil
	}
	switch gate.kind {
	case approvalGate:
		var outcome alfredv1.ApprovalOutcome
		switch message.String() {
		case "y":
			outcome = alfredv1.ApprovalOutcome_APPROVAL_OUTCOME_APPROVED
		case "n":
			outcome = alfredv1.ApprovalOutcome_APPROVAL_OUTCOME_DENIED
		default:
			return model, nil
		}
		model.resolvingGate = gate.key()
		model.actionErr = nil
		model.resizeTranscript()
		return model, model.resolveApproval(gate, outcome)
	case inputGate:
		if key.Matches(message, model.keys.submit) {
			input := strings.TrimSpace(model.gateInput.Value())
			if input == "" {
				return model, nil
			}
			model.resolvingGate = gate.key()
			model.actionErr = nil
			model.resizeTranscript()
			return model, model.provideInput(gate, input)
		}
		var command tea.Cmd
		model.gateInput, command = model.gateInput.Update(message)
		return model, command
	default:
		return model, nil
	}
}

func (model Model) inputView() string {
	gate, ok := model.gates.current()
	if !ok {
		return model.composer.View()
	}
	status := ""
	if model.resolvingGate == gate.key() {
		if gate.kind == approvalGate {
			status = "Submitting decision…"
		} else {
			status = "Submitting response…"
		}
	}
	return renderInteractionGate(
		gate,
		model.gateInput.View(),
		status,
		model.composer.input.Width(),
		strings.Join(model.keys.interrupt.Keys(), "/"),
	)
}

func (model Model) currentGateKey() string {
	gate, ok := model.gates.current()
	if !ok {
		return ""
	}
	return gate.key()
}

func (model Model) hasGateKind(kind gateKind) bool {
	gate, ok := model.gates.current()
	return ok && gate.kind == kind
}

func (model *Model) syncGateFocus(previousGate string) tea.Cmd {
	gate, ok := model.gates.current()
	nextGate := ""
	if ok {
		nextGate = gate.key()
	}
	if previousGate == nextGate {
		return nil
	}
	model.resolvingGate = ""
	model.gateInput.Reset()
	model.gateInput.Blur()
	model.composer.Blur()
	return model.focusInput()
}

func (model Model) setProjectSkillTrust(trust alfredv1.ProjectSkillTrust) tea.Cmd {
	return func() tea.Msg {
		_, err := model.clients.Project.SetProjectSkillTrust(
			model.ctx,
			&alfredv1.SetProjectSkillTrustRequest{
				ProjectId: model.project.GetId(),
				Trust:     trust,
			},
		)
		return projectSkillTrustSetMsg{trust: trust, err: err}
	}
}

func (model Model) requiresSkillTrust() bool {
	return model.projectSkills != nil && len(model.projectSkills.GetRoots()) > 0 &&
		model.projectSkills.GetTrust() == alfredv1.ProjectSkillTrust_PROJECT_SKILL_TRUST_UNKNOWN
}

func (model Model) toggledSkillTrust() alfredv1.ProjectSkillTrust {
	if model.project.GetSkillTrust() == alfredv1.ProjectSkillTrust_PROJECT_SKILL_TRUST_TRUSTED {
		return alfredv1.ProjectSkillTrust_PROJECT_SKILL_TRUST_DENIED
	}
	return alfredv1.ProjectSkillTrust_PROJECT_SKILL_TRUST_TRUSTED
}

// Run starts the terminal application and blocks until it exits.
func Run(ctx context.Context, config Config, clients rpc.Clients, root string) error {
	const op = "Run"

	if _, err := tea.NewProgram(NewModel(ctx, config, clients, root), tea.WithContext(ctx)).Run(); err != nil {
		return errors.B.Op(op).Text("run terminal client").Err(err).Build()
	}
	return nil
}

var _ tea.Model = Model{}
