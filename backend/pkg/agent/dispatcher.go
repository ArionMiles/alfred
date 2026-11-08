package agent

import (
	"context"
	"sync"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

// Dispatcher coordinates active executions around a stateless Runner.
type Dispatcher struct {
	lifetime context.Context
	runner   *Runner
	mu       sync.Mutex
	active   map[alfred.RunID]*activeRun
}

type activeRun struct {
	cancel context.CancelFunc
	done   chan struct{}

	cancelRequested bool
	result          error
}

// NewDispatcher creates a dispatcher whose executions are bounded by lifetime.
func NewDispatcher(lifetime context.Context, runner *Runner) *Dispatcher {
	return &Dispatcher{
		lifetime: lifetime,
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
	if err := dispatcher.lifetime.Err(); err != nil {
		return errors.B.
			Op(op).
			Textf("dispatch run %q after service stopped", runID).
			Err(err).
			Build()
	}

	dispatcher.mu.Lock()
	if _, exists := dispatcher.active[runID]; exists {
		dispatcher.mu.Unlock()
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
	dispatcher.mu.Unlock()

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

	dispatcher.mu.Lock()
	if !active.cancelRequested {
		if dispatcher.active[runID] == active {
			delete(dispatcher.active, runID)
		}
		close(active.done)
		dispatcher.mu.Unlock()
		return
	}
	dispatcher.mu.Unlock()

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
	if err := dispatcher.runner.provideInput(ctx, runID, response); err != nil {
		return err
	}
	return dispatcher.redispatch(runID)
}

// ResumeRun validates and redispatches an interrupted run.
func (dispatcher *Dispatcher) ResumeRun(ctx context.Context, runID alfred.RunID) error {
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

	dispatcher.mu.Lock()
	active := dispatcher.active[runID]
	if active == nil {
		active = &activeRun{
			done:            make(chan struct{}),
			cancelRequested: true,
		}
		dispatcher.active[runID] = active
		dispatcher.mu.Unlock()

		err := dispatcher.cancelStoppedRun(context.WithoutCancel(ctx), runID)
		dispatcher.completeActiveRun(runID, active, err)
		return err
	}
	if !active.cancelRequested {
		active.cancelRequested = true
		active.cancel()
	}
	done := active.done
	dispatcher.mu.Unlock()

	select {
	case <-done:
		return active.result
	case <-ctx.Done():
		return errors.B.Op(op).Textf("wait for run %q cancellation", runID).Err(ctx.Err()).Build()
	}
}

// redispatch waits only for the execution that exposed a durable continuation
// point. Once a response is stored, request cancellation must not strand it.
func (dispatcher *Dispatcher) redispatch(runID alfred.RunID) error {
	const op = "redispatch"

	for {
		dispatcher.mu.Lock()
		active := dispatcher.active[runID]
		if active == nil {
			dispatcher.mu.Unlock()
			return dispatcher.Dispatch(dispatcher.lifetime, runID)
		}
		if active.cancelRequested {
			done := active.done
			dispatcher.mu.Unlock()
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
		dispatcher.mu.Unlock()

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
	dispatcher.mu.Lock()
	active.result = result
	if dispatcher.active[runID] == active {
		delete(dispatcher.active, runID)
	}
	close(active.done)
	dispatcher.mu.Unlock()
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
