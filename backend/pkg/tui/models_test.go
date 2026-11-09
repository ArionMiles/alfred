package tui

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectinprocess"
	"go.uber.org/mock/gomock"

	"github.com/ArionMiles/alfred"
	alfredv1 "github.com/ArionMiles/alfred/gen/alfred/v1"
	"github.com/ArionMiles/alfred/gen/alfred/v1/alfredv1connect"
	"github.com/ArionMiles/alfred/mocks"
	"github.com/ArionMiles/alfred/pkg/providers"
	"github.com/ArionMiles/alfred/pkg/storage/sqlite"
	"github.com/ArionMiles/alfred/pkg/transport/rpc"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

func TestModelPickerLoadsAndSavesThroughBothEntryPoints(t *testing.T) {
	t.Parallel()
	for _, entry := range []string{"slash", "alias", "palette"} {
		t.Run(entry, func(t *testing.T) {
			t.Parallel()
			model, store, provider := newModelPickerTestModel(t)
			first := alfred.ModelRef{ProviderID: "openai", ModelID: "a"}
			if err := store.SetSelectedModel(t.Context(), first); err != nil {
				t.Fatal(err)
			}
			model.composer.SetValue("conversation draft")
			model.activeRun = &alfredv1.Run{Id: "existing", DefaultModel: &alfredv1.ModelRef{ProviderId: "openai", ModelId: "a"}, Status: alfredv1.RunStatus_RUN_STATUS_RUNNING}
			if entry == "palette" {
				opened, _ := model.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
				model = opened.(Model)
				for i, item := range model.palette.Items() {
					if item.(command).id == modelsCommand {
						model.palette.Select(i)
					}
				}
			} else {
				name := "/models"
				if entry == "alias" {
					name = "/model"
				}
				model.composer.SetValue(name)
			}
			opened, load := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			model = opened.(Model)
			if !strings.Contains(model.View().Content, "Loading models") {
				t.Fatal("model picker did not show loading state")
			}
			loaded, _ := model.Update(load())
			model = loaded.(Model)
			if len(model.palette.Items()) != 2 || !model.palette.Items()[0].(modelItem).current || !strings.Contains(model.palette.Items()[0].(modelItem).Title(), "✓") {
				t.Fatal("saved model was not marked")
			}
			for _, key := range []tea.KeyPressMsg{{Code: tea.KeyTab}, {Code: tea.KeyTab, Mod: tea.ModShift}, {Code: tea.KeyDown}, {Code: tea.KeyUp}} {
				updated, _ := model.Update(key)
				model = updated.(Model)
			}
			if model.palette.Index() != 0 {
				t.Fatal("arrow and tab navigation did not return to the saved model")
			}
			moved, _ := model.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
			model = moved.(Model)
			if model.palette.SelectedItem().(modelItem).info.GetRef().GetModelId() != "z" {
				t.Fatal("mouse wheel did not navigate model choices")
			}
			provider.EXPECT().ResolveModel(gomock.Any(), alfred.ModelID("z")).Return(pickerModelInfo("z", "Zulu"), nil)
			saving, save := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			model = saving.(Model)
			for _, key := range []tea.KeyPressMsg{{Code: tea.KeyEscape}, {Code: 'p', Mod: tea.ModCtrl}} {
				unchanged, _ := model.Update(key)
				if unchanged.(Model).models == nil || !unchanged.(Model).models.saving {
					t.Fatal("pending selection was dismissed")
				}
			}
			saved, _ := model.Update(save())
			model = saved.(Model)
			selected, err := store.GetSelectedModel(t.Context())
			if err != nil || selected.ModelID != "z" || model.palette != nil || model.models != nil || !model.composer.Focused() {
				t.Fatalf("selection=%v error=%v", selected, err)
			}
			if model.activeRun.GetDefaultModel().GetModelId() != "a" {
				t.Fatal("choosing a default modified the active run")
			}
			if entry == "palette" && model.composer.Value() != "conversation draft" {
				t.Fatal("model picker changed the conversation draft")
			}
			reopened, load := model.openModels()
			loaded, _ = reopened.(Model).Update(load())
			if !loaded.(Model).palette.SelectedItem().(modelItem).current || loaded.(Model).palette.Index() != 1 {
				t.Fatal("reopened picker did not read the persisted selection")
			}
		})
	}
}

func TestModelPickerFilteringAndEscapePreserveRequestedInput(t *testing.T) {
	t.Parallel()
	model, _, _ := newModelPickerTestModel(t)
	model.composer.SetValue("message draft")
	model.gates.apply(inputRequestedEvent(1, "run-1", "input-1", "Question"))
	model.gateInput.SetValue("response draft")
	opened, load := model.openModels()
	loaded, _ := opened.(Model).Update(load())
	model = loaded.(Model)
	model.palette.SetFilterText("Zulu")
	if len(model.palette.VisibleItems()) != 1 || model.palette.SelectedItem().(modelItem).info.GetRef().GetModelId() != "z" {
		t.Fatal("model filtering did not narrow choices")
	}
	model.palette.SetFilterState(list.Filtering)
	accepted, command := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = accepted.(Model)
	if model.models.saving || model.palette.FilterState() != list.FilterApplied {
		t.Fatal("accepting filter started a save")
	}
	_ = command
	cleared, _ := model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	model = cleared.(Model)
	if model.models == nil || model.palette.FilterState() != list.Unfiltered {
		t.Fatal("Escape did not clear the applied filter")
	}
	closed, _ := model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	model = closed.(Model)
	if model.models != nil || model.palette != nil || model.composer.Value() != "message draft" || model.gateInput.Value() != "response draft" || !model.gateInput.Focused() {
		t.Fatal("picker did not preserve drafts and restore requested-input focus")
	}
}

func TestModelPickerSaveFailureKeepsChoicesForRetry(t *testing.T) {
	const op = "TestModelPickerSaveFailureKeepsChoicesForRetry"
	t.Parallel()
	model, store, provider := newModelPickerTestModel(t)
	opened, load := model.openModels()
	loaded, _ := opened.(Model).Update(load())
	model = loaded.(Model)
	gomock.InOrder(
		provider.EXPECT().ResolveModel(gomock.Any(), alfred.ModelID("a")).Return(alfred.ModelInfo{}, errors.B.Op(op).KindUnavailable().UserMsg("Try again shortly.").Build()),
		provider.EXPECT().ResolveModel(gomock.Any(), alfred.ModelID("a")).Return(pickerModelInfo("a", "Alpha"), nil),
	)
	saving, save := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	failed, _ := saving.(Model).Update(save())
	model = failed.(Model)
	if model.models.saving || len(model.palette.Items()) != 2 || !strings.Contains(model.View().Content, "Try again shortly") {
		t.Fatal("failed save lost choices or safe feedback")
	}
	if _, err := store.GetSelectedModel(t.Context()); errors.WhatKind(err) != errors.NotFound {
		t.Fatal("failed selection changed the preference")
	}
	saving, save = model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	saved, _ := saving.(Model).Update(save())
	if saved.(Model).models != nil {
		t.Fatal("retry did not close picker after success")
	}
}

func TestModelPickerLoadingEmptyErrorAndUnavailableStates(t *testing.T) {
	const op = "TestModelPickerLoadingEmptyErrorAndUnavailableStates"
	t.Parallel()
	model, store, _ := newModelPickerTestModel(t)
	opened, load := model.openModels()
	old := opened.(Model).models
	closed, _ := opened.(Model).Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	reopened, _ := closed.(Model).openModels()
	stale, _ := reopened.(Model).Update(load())
	model = stale.(Model)
	if model.models == old || !model.models.loading || len(model.palette.Items()) != 0 {
		t.Fatal("dismissed picker response affected the reopened picker")
	}
	empty, _ := model.Update(modelsListedMsg{picker: model.models})
	model = empty.(Model)
	if !strings.Contains(model.View().Content, "Connect a model provider") || !strings.Contains(model.View().Content, "/providers") ||
		strings.Contains(model.View().Content, "OpenAI") {
		t.Fatal("empty picker did not explain how to set up a provider")
	}
	opened, _ = model.openModels()
	failed, _ := opened.(Model).Update(modelsListedMsg{picker: opened.(Model).models, err: errors.B.Op(op).KindUnavailable().UserMsg("Catalog is offline.").Build()})
	if !strings.Contains(failed.(Model).View().Content, "Catalog is offline") {
		t.Fatal("load failure did not show useful feedback")
	}
	if err := store.SetSelectedModel(t.Context(), alfred.ModelRef{ProviderID: "removed", ModelID: "missing"}); err != nil {
		t.Fatal(err)
	}
	opened, load = model.openModels()
	loaded, _ := opened.(Model).Update(load())
	if !strings.Contains(loaded.(Model).View().Content, "previously selected model is unavailable") {
		t.Fatal("unavailable saved selection was silently replaced")
	}
}

func TestModelRowsKeepProviderLabelsAlignedAndBounded(t *testing.T) {
	t.Parallel()
	for _, width := range []int{1, 5, 20, 64} {
		items := []list.Item{
			modelItem{info: &alfredv1.ModelInfo{Name: "Alpha", ProviderName: "OpenAI"}},
			modelItem{info: &alfredv1.ModelInfo{Name: "長いモデル名 with a long suffix", ProviderName: "Another provider with a long label"}},
		}
		palette := newPickerList("Models", items, width, 8)
		for index, item := range items {
			var rendered bytes.Buffer
			(modelDelegate{}).Render(&rendered, palette, index, item)
			row := rendered.String()
			if lipgloss.Width(row) > width || strings.Contains(row, "\n") {
				t.Fatalf("width=%d row=%q", width, row)
			}
			if width == 64 && index == 0 {
				labelStart := strings.Index(row, "OpenAI")
				if labelStart < 0 || lipgloss.Width(row[:labelStart]) != width-lipgloss.Width("OpenAI") {
					t.Fatalf("provider label is not right aligned: %q", row)
				}
			}
		}
	}
}

func pickerModelInfo(id, name string) alfred.ModelInfo {
	return alfred.ModelInfo{Ref: alfred.ModelRef{ProviderID: "openai", ModelID: alfred.ModelID(id)}, Name: name, Capabilities: alfred.ModelCapabilities{InputModalities: []alfred.ContentKind{alfred.ContentKindText}, OutputModalities: []alfred.ContentKind{alfred.ContentKindText}}}
}

func newModelPickerTestModel(t *testing.T) (Model, *sqlite.Store, *mocks.MockModelProvider) {
	t.Helper()
	store, err := sqlite.Open(t.Context(), sqlite.Config{Path: filepath.Join(t.TempDir(), "alfred.db"), BusyTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := store.PutProviderInstance(t.Context(), alfred.ProviderInstance{ID: "openai", Type: "openai", Name: "OpenAI", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	provider := mocks.NewMockModelProvider(gomock.NewController(t))
	provider.EXPECT().ID().Return(alfred.ProviderID("openai"))
	provider.EXPECT().Models(gomock.Any()).Return([]alfred.ModelInfo{pickerModelInfo("a", "Alpha"), pickerModelInfo("z", "Zulu")}, nil).AnyTimes()
	registry, err := providers.NewRegistry([]alfred.ModelProvider{provider})
	if err != nil {
		t.Fatal(err)
	}
	server := connect.NewServer()
	alfredv1connect.RegisterModelServiceHandler(server, rpc.NewModelHandler(providers.NewModelService(store, registry, store)))
	model := readyModel(t)
	model.clients = rpc.NewClients(connect.NewClient(connectinprocess.New(server)))
	resized, _ := model.Update(tea.WindowSizeMsg{Width: 90, Height: 24})
	return resized.(Model), store, provider
}
