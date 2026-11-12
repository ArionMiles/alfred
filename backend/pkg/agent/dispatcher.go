package agent

import (
	"context"
	"maps"
	"sync"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

// Dispatcher coordinates active executions around a stateless Runner.
type Dispatcher struct {
	lifetime context.Context
	stop     context.CancelFunc
	runner   *Runner
	// activeMu protects admission, execution membership, and cancellation state.
	activeMu sync.Mutex
	stopping bool
	// During shutdown, completed entries remain until their durable state is settled.
	active map[alfred.RunID]*activeRun
}

type activeRun struct {
	cancel context.CancelFunc
	done   chan struct{}

	cancelRequested bool
	result          error
}

// NewDispatcher creates a dispatcher that owns its execution lifetime.
func NewDispatcher(runner *Runner) *Dispatcher {
	lifetime, stop := context.WithCancel(context.Background())
	return &Dispatcher{
		lifetime: lifetime,
		stop:     stop,
		runner:   runner,
		active:   make(map[alfred.RunID]*activeRun),
	}
}

// Dispatch accepts a run for background execution and makes it addressable for
// cancellation. The caller's context bounds dispatch, not run execution.
func (dispatcher *Dispatcher) Dispatch(ctx context.Context, runID alfred.RunID) error {
	const op = "Dispatch"

	if err := ctx.Err(); err != nil {
		return errors.B.Op(op).Textf("dispatch run %q", runID).Err(err).Build()
	}
	dispatcher.activeMu.Lock()
	if dispatcher.stopping {
		dispatcher.activeMu.Unlock()
		return errors.B.Op(op).KindFailedPrecondition().UserMsg("Alfred is shutting down.").Build()
	}
	if _, exists := dispatcher.active[runID]; exists {
		dispatcher.activeMu.Unlock()
		return errors.B.
			Op(op).
			KindConflict().
			Textf("run %q is already active", runID).
			UserMsg("this run is already active").
			Build()
	}
	runContext, cancel := context.WithCancel(dispatcher.lifetime)
	active := &activeRun{
		cancel: cancel,
		done:   make(chan struct{}),
	}
	dispatcher.active[runID] = active
	dispatcher.activeMu.Unlock()

	go dispatcher.execute(runContext, runID, active)
	return nil
}

func (dispatcher *Dispatcher) execute(
	ctx context.Context,
	runID alfred.RunID,
	active *activeRun,
) {
	_ = dispatcher.runner.Execute(ctx, runID)
	active.cancel()

	dispatcher.activeMu.Lock()
	if !active.cancelRequested {
		if !dispatcher.stopping && dispatcher.active[runID] == active {
			delete(dispatcher.active, runID)
		}
		close(active.done)
		dispatcher.activeMu.Unlock()
		return
	}
	dispatcher.activeMu.Unlock()

	dispatcher.completeActiveRun(
		runID,
		active,
		dispatcher.cancelStoppedRun(context.WithoutCancel(ctx), runID),
	)
}

// ResolveApproval records an approval decision and redispatches the run.
func (dispatcher *Dispatcher) ResolveApproval(
	ctx context.Context,
	runID alfred.RunID,
	resolution alfred.ApprovalResolution,
) error {
	if err := dispatcher.acceptControl(); err != nil {
		return err
	}

	if err := dispatcher.runner.resolveApproval(ctx, runID, resolution); err != nil {
		return err
	}
	return dispatcher.redispatch(runID)
}

// ProvideInput records requested input and redispatches the run.
func (dispatcher *Dispatcher) ProvideInput(
	ctx context.Context,
	runID alfred.RunID,
	response alfred.InputResponse,
) error {
	if err := dispatcher.acceptControl(); err != nil {
		return err
	}

	if err := dispatcher.runner.provideInput(ctx, runID, response); err != nil {
		return err
	}
	return dispatcher.redispatch(runID)
}

// ResumeRun validates and redispatches an interrupted run.
func (dispatcher *Dispatcher) ResumeRun(ctx context.Context, runID alfred.RunID) error {
	if err := dispatcher.acceptControl(); err != nil {
		return err
	}

	if err := dispatcher.runner.resumeRun(ctx, runID); err != nil {
		return err
	}
	return dispatcher.redispatch(runID)
}

// CancelRun stops active execution before recording the terminal transition, so
// no runner writes can follow the cancellation event.
func (dispatcher *Dispatcher) CancelRun(ctx context.Context, runID alfred.RunID) error {
	const op = "CancelRun"

	if err := ctx.Err(); err != nil {
		return errors.B.Op(op).Textf("cancel run %q", runID).Err(err).Build()
	}

	dispatcher.activeMu.Lock()
	if dispatcher.stopping {
		dispatcher.activeMu.Unlock()
		return errors.B.Op(op).KindFailedPrecondition().UserMsg("Alfred is shutting down.").Build()
	}

	active := dispatcher.active[runID]
	if active == nil {
		active = &activeRun{
			done:            make(chan struct{}),
			cancelRequested: true,
		}
		dispatcher.active[runID] = active
		dispatcher.activeMu.Unlock()

		err := dispatcher.cancelStoppedRun(context.WithoutCancel(ctx), runID)
		dispatcher.completeActiveRun(runID, active, err)
		return err
	}
	if !active.cancelRequested {
		active.cancelRequested = true
		active.cancel()
	}
	done := active.done
	dispatcher.activeMu.Unlock()

	select {
	case <-done:
		return active.result
	case <-ctx.Done():
		return errors.B.Op(op).Textf("wait for run %q cancellation", runID).Err(ctx.Err()).Build()
	}
}

// Shutdown leaves storage open and retains unsettled executions when its context expires.
func (dispatcher *Dispatcher) Shutdown(ctx context.Context) error {
	const op = "Shutdown"
	dispatcher.activeMu.Lock()
	dispatcher.stopping = true
	activeRuns := maps.Clone(dispatcher.active)
	dispatcher.activeMu.Unlock()
	dispatcher.stop()
	for runID, active := range activeRuns {
		select {
		case <-active.done:
		case <-ctx.Done():
			return errors.B.Op(op).Textf("wait for run %q shutdown", runID).Err(ctx.Err()).Build()
		}
		if err := dispatcher.interruptStoppedRun(ctx, runID); err != nil {
			return errors.B.Op(op).Err(err).Build()
		}
		dispatcher.activeMu.Lock()
		delete(dispatcher.active, runID)
		dispatcher.activeMu.Unlock()
	}
	return nil
}

func (dispatcher *Dispatcher) acceptControl() error {
	const op = "acceptControl"
	dispatcher.activeMu.Lock()
	defer dispatcher.activeMu.Unlock()
	if dispatcher.stopping {
		return errors.B.Op(op).KindFailedPrecondition().UserMsg("Alfred is shutting down.").Build()
	}
	return nil
}

func (dispatcher *Dispatcher) interruptStoppedRun(ctx context.Context, runID alfred.RunID) error {
	const op = "interruptStoppedRun"
	run, err := dispatcher.runner.runs.GetRun(ctx, runID)
	if err != nil {
		return errors.B.Op(op).Textf("get stopped run %q", runID).Err(err).Build()
	}
	if run.Status != alfred.RunStatusQueued && run.Status != alfred.RunStatusRunning {
		return nil
	}
	if _, err := dispatcher.runner.events.Append(ctx, alfred.NewRunEvent{
		RunID: runID, Visibility: alfred.RunEventVisibilityUser,
		Payload: alfred.RunInterrupted{
			Reason:  errors.Unavailable.Code,
			Message: "The service is shutting down. Resume this run after reconnecting.",
		},
	}); err != nil {
		return errors.B.Op(op).Textf("record shutdown interruption for run %q", runID).Err(err).Build()
	}
	if err := dispatcher.runner.transitionRun(ctx, runID, run.Status, alfred.RunStatusInterrupted); err != nil {
		return errors.B.Op(op).Textf("interrupt stopped run %q", runID).Err(err).Build()
	}
	return nil
}

// redispatch waits only for the execution that exposed a durable continuation
// point. Once a response is stored, request cancellation must not strand it.
func (dispatcher *Dispatcher) redispatch(runID alfred.RunID) error {
	const op = "redispatch"

	for {
		dispatcher.activeMu.Lock()
		active := dispatcher.active[runID]
		if active == nil {
			dispatcher.activeMu.Unlock()
			return dispatcher.Dispatch(dispatcher.lifetime, runID)
		}
		if active.cancelRequested {
			done := active.done
			dispatcher.activeMu.Unlock()
			select {
			case <-done:
				return errors.B.
					Op(op).
					KindConflict().
					Textf("run %q was cancelled before redispatch", runID).
					UserMsg("this run was cancelled").
					Build()
			case <-dispatcher.lifetime.Done():
				return errors.B.Op(op).Textf("redispatch run %q after service stopped", runID).
					Err(dispatcher.lifetime.Err()).Build()
			}
		}
		done := active.done
		dispatcher.activeMu.Unlock()

		select {
		case <-done:
		case <-dispatcher.lifetime.Done():
			return errors.B.Op(op).Textf("redispatch run %q after service stopped", runID).
				Err(dispatcher.lifetime.Err()).Build()
		}
	}
}

func (dispatcher *Dispatcher) completeActiveRun(
	runID alfred.RunID,
	active *activeRun,
	result error,
) {
	dispatcher.activeMu.Lock()
	active.result = result
	if !dispatcher.stopping && dispatcher.active[runID] == active {
		delete(dispatcher.active, runID)
	}
	close(active.done)
	dispatcher.activeMu.Unlock()
}

func (dispatcher *Dispatcher) cancelStoppedRun(ctx context.Context, runID alfred.RunID) error {
	const op = "cancelStoppedRun"

	run, err := dispatcher.runner.runs.GetRun(ctx, runID)
	if err != nil {
		return errors.B.Op(op).Textf("get run %q", runID).Err(err).Build()
	}
	switch run.Status {
	case alfred.RunStatusCompleted, alfred.RunStatusFailed, alfred.RunStatusCancelled:
		return errors.B.
			Op(op).
			KindConflict().
			Textf("run %q has terminal status %q", runID, run.Status).
			UserMsg("this run has already ended").
			Build()
	}
	if err := dispatcher.runner.transitionRun(
		ctx,
		runID,
		run.Status,
		alfred.RunStatusCancelled,
	); err != nil {
		return errors.B.Op(op).Textf("cancel run %q", runID).Err(err).Build()
	}
	return nil
}

var (
	_ alfred.RunDispatcher = (*Dispatcher)(nil)
	_ alfred.RunControl    = (*Dispatcher)(nil)
)
