package a2a

import (
	"context"
	"errors"
	"time"

	sdka2a "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/a2aproject/a2a-go/v2/a2asrv/eventqueue"
)

const (
	cancelRetryWindow   = 2 * time.Second
	cancelRetryInterval = 5 * time.Millisecond
)

// cancelRetryHandler works around a2asrv cancelling a task whose execution is
// still registered by signalling on that execution's own event queue. A parked
// task's execution has just ended (the park yields INPUT_REQUIRED and returns),
// so until a2asrv cleans the execution up that signal fails with
// eventqueue.ErrQueueClosed or ErrTaskNotCancelable (a2a-go's local task
// manager documents this as a known TODO). Once the cleanup ran, Cancel takes
// the no-live-execution path, which works. So a failed cancel of a task that is
// still non-terminal is retried for a bounded window; a terminal task keeps its
// original error immediately.
type cancelRetryHandler struct {
	a2asrv.RequestHandler
	window, interval time.Duration
}

func newCancelRetryHandler(h a2asrv.RequestHandler) a2asrv.RequestHandler {
	return cancelRetryHandler{RequestHandler: h, window: cancelRetryWindow, interval: cancelRetryInterval}
}

// CancelTask implements a2asrv.RequestHandler.
func (h cancelRetryHandler) CancelTask(ctx context.Context, req *sdka2a.CancelTaskRequest) (*sdka2a.Task, error) {
	task, err := h.RequestHandler.CancelTask(ctx, req)
	if !retryableCancelErr(err) {
		return task, err
	}
	deadline := time.Now().Add(h.window)
	for time.Now().Before(deadline) {
		cur, getErr := h.RequestHandler.GetTask(ctx, &sdka2a.GetTaskRequest{Tenant: req.Tenant, ID: req.ID})
		if getErr != nil || cur.Status.State.Terminal() {
			return task, err
		}
		select {
		case <-ctx.Done():
			return nil, err
		case <-time.After(h.interval):
		}
		task, err = h.RequestHandler.CancelTask(ctx, req)
		if !retryableCancelErr(err) {
			return task, err
		}
	}
	return task, err
}

func retryableCancelErr(err error) bool {
	return errors.Is(err, sdka2a.ErrTaskNotCancelable) || errors.Is(err, eventqueue.ErrQueueClosed)
}
