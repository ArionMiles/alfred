package tui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectinprocess"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/gen/alfred/v1/alfredv1connect"
	"github.com/ArionMiles/alfred/pkg/identity"
	"github.com/ArionMiles/alfred/pkg/storage/sqlite"
	"github.com/ArionMiles/alfred/pkg/tools"
	"github.com/ArionMiles/alfred/pkg/transport/rpc"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

func TestSearchSetupValidatesSavesAndReopensThroughBothEntryPoints(t *testing.T) {
	t.Parallel()
	for _, entry := range []string{"slash", "palette"} {
		t.Run(entry, func(t *testing.T) {
			t.Parallel()
			model, store := newSearchTestModel(t)
			model.composer.SetValue("draft")
			if entry == "slash" {
				model.composer.SetValue("/web-search")
			} else {
				opened, _ := model.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
				model = opened.(Model)
				for index, item := range model.palette.Items() {
					if item.(command).id == searchSetupCommand {
						model.palette.Select(index)
					}
				}
			}
			opened, load := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			loaded, _ := opened.(Model).Update(load())
			model = loaded.(Model)
			if model.searchSetup == nil || model.providers != nil {
				t.Fatal("search setup used model Providers")
			}
			selected, prepare := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			prepared, _ := selected.(Model).Update(prepare())
			model = prepared.(Model)
			if model.searchSetup.form.fields[0].input.EchoMode != textinput.EchoPassword {
				t.Fatal("key input is not masked")
			}
			model.searchSetup.form.focus(len(model.searchSetup.form.fields))
			saving, save := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			validated, _ := saving.(Model).Update(save())
			model = validated.(Model)
			if len(model.searchSetup.form.problems) != 1 {
				t.Fatal("required key validation missing")
			}
			model.searchSetup.form.fields[0].input.SetValue("saved-secret")
			model.searchSetup.form.focus(len(model.searchSetup.form.fields))
			saving, save = model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			blocked, _ := saving.(Model).Update(tea.KeyPressMsg{Code: tea.KeyEscape})
			if blocked.(Model).searchSetup == nil {
				t.Fatal("save was dismissed in flight")
			}
			saved, _ := saving.(Model).Update(save())
			model = saved.(Model)
			if !model.searchSetup.saved || model.searchSetup.form != nil || !strings.Contains(model.View().Content, "Search settings saved") {
				t.Fatal("successful save retained secret inputs")
			}
			if _, err := store.GetProviderInstance(t.Context(), "exa"); errors.WhatKind(err) != errors.NotFound {
				t.Fatal("search save polluted model metadata")
			}
			if _, err := store.GetSearchSettings(t.Context(), "exa"); err != nil {
				t.Fatal(err)
			}
			closed, _ := model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
			model = closed.(Model)
			if model.palette != nil || !model.composer.Focused() || (entry == "palette" && model.composer.Value() != "draft") {
				t.Fatal("closing search setup changed draft or focus")
			}
			opened, load = model.openSearchSetup()
			loaded, _ = opened.(Model).Update(load())
			selected, prepare = loaded.(Model).Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			prepared, _ = selected.(Model).Update(prepare())
			if prepared.(Model).searchSetup.form.fields[0].input.Value() != "" {
				t.Fatal("saved key was exposed on reopening")
			}
		})
	}
}

func TestSearchSetupIgnoresDismissedLoadsAndPreservesFailedSaveInputs(t *testing.T) {
	const op = "TestSearchSetupIgnoresDismissedLoadsAndPreservesFailedSaveInputs"
	t.Parallel()
	model, _ := newSearchTestModel(t)
	opened, load := model.openSearchSetup()
	closed, _ := opened.(Model).Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	ignored, _ := closed.(Model).Update(load())
	if ignored.(Model).searchSetup != nil || ignored.(Model).palette != nil {
		t.Fatal("dismissed search setup reopened")
	}
	opened, load = model.openSearchSetup()
	loaded, _ := opened.(Model).Update(load())
	selected, prepare := loaded.(Model).Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	prepared, _ := selected.(Model).Update(prepare())
	model = prepared.(Model)
	model.searchSetup.form.fields[0].input.SetValue("unsaved-secret")
	model.searchSetup.saving = true
	failed, _ := model.Update(searchSavedMsg{owner: model.searchSetup, err: errors.B.Op(op).KindUnavailable().UserMsg("Try again.").Build()})
	model = failed.(Model)
	if model.searchSetup.saving || model.searchSetup.form.fields[0].input.Value() != "unsaved-secret" || !strings.Contains(model.View().Content, "Try again") {
		t.Fatal("failed save lost form or feedback")
	}
}

func newSearchTestModel(t *testing.T) (Model, *sqlite.Store) {
	t.Helper()
	store, err := sqlite.Open(t.Context(), sqlite.Config{Path: filepath.Join(t.TempDir(), "alfred.db"), BusyTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	server := connect.NewServer()
	alfredv1connect.RegisterSearchSetupServiceHandler(server, rpc.NewSearchSetupHandler(tools.NewSearchSetup(tools.SearchSetupDefinitions(), store, store, identity.Generator[alfred.CredentialRef]{})))
	model := readyModel(t)
	model.clients = rpc.NewClients(connect.NewClient(connectinprocess.New(server)))
	resized, _ := model.Update(tea.WindowSizeMsg{Width: 90, Height: 24})
	return resized.(Model), store
}
