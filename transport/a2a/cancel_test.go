package a2a

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	sdka2a "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/a2aproject/a2a-go/v2/a2asrv/eventqueue"
)

type stubCancelHandler struct {
	a2asrv.RequestHandler
	cancelErrs []error // returned in order; success after they run out
	state      sdka2a.TaskState
	cancels    int
}

func (s *stubCancelHandler) CancelTask(context.Context, *sdka2a.CancelTaskRequest) (*sdka2a.Task, error) {
	s.cancels++
	if s.cancels <= len(s.cancelErrs) {
		return nil, s.cancelErrs[s.cancels-1]
	}
	return &sdka2a.Task{Status: sdka2a.TaskStatus{State: sdka2a.TaskStateCanceled}}, nil
}

func (s *stubCancelHandler) GetTask(context.Context, *sdka2a.GetTaskRequest) (*sdka2a.Task, error) {
	return &sdka2a.Task{Status: sdka2a.TaskStatus{State: s.state}}, nil
}

func retrying(s *stubCancelHandler, window time.Duration) a2asrv.RequestHandler {
	return cancelRetryHandler{RequestHandler: s, window: window, interval: time.Millisecond}
}

func TestCancelRetry_RetriesNonTerminalThenSucceeds(t *testing.T) {
	s := &stubCancelHandler{
		state: sdka2a.TaskStateInputRequired,
		cancelErrs: []error{
			fmt.Errorf("cancelation failed: %w", eventqueue.ErrQueueClosed),
			fmt.Errorf("non-cancelable: %w", sdka2a.ErrTaskNotCancelable),
		},
	}
	task, err := retrying(s, time.Second).CancelTask(context.Background(), &sdka2a.CancelTaskRequest{ID: "t"})
	if err != nil || task.Status.State != sdka2a.TaskStateCanceled {
		t.Fatalf("got (%v, %v), want CANCELED", task, err)
	}
	if s.cancels != 3 {
		t.Errorf("cancel calls = %d, want 3", s.cancels)
	}
}

func TestCancelRetry_TerminalTaskKeepsOriginalError(t *testing.T) {
	s := &stubCancelHandler{state: sdka2a.TaskStateCompleted, cancelErrs: []error{sdka2a.ErrTaskNotCancelable}}
	_, err := retrying(s, time.Second).CancelTask(context.Background(), &sdka2a.CancelTaskRequest{ID: "t"})
	if !errors.Is(err, sdka2a.ErrTaskNotCancelable) {
		t.Fatalf("err = %v, want ErrTaskNotCancelable", err)
	}
	if s.cancels != 1 {
		t.Errorf("cancel calls = %d, want 1 (no retry)", s.cancels)
	}
}

func TestCancelRetry_GivesUpAfterWindow(t *testing.T) {
	s := &stubCancelHandler{state: sdka2a.TaskStateInputRequired}
	for i := 0; i < 1000; i++ {
		s.cancelErrs = append(s.cancelErrs, sdka2a.ErrTaskNotCancelable)
	}
	start := time.Now()
	_, err := retrying(s, 30*time.Millisecond).CancelTask(context.Background(), &sdka2a.CancelTaskRequest{ID: "t"})
	if !errors.Is(err, sdka2a.ErrTaskNotCancelable) {
		t.Fatalf("err = %v, want ErrTaskNotCancelable", err)
	}
	if time.Since(start) > time.Second {
		t.Errorf("gave up after %v, want ~30ms", time.Since(start))
	}
}

func TestCancelRetry_OtherErrorsNotRetried(t *testing.T) {
	s := &stubCancelHandler{state: sdka2a.TaskStateInputRequired, cancelErrs: []error{errors.New("boom")}}
	if _, err := retrying(s, time.Second).CancelTask(context.Background(), &sdka2a.CancelTaskRequest{ID: "t"}); err == nil {
		t.Fatal("want error")
	}
	if s.cancels != 1 {
		t.Errorf("cancel calls = %d, want 1", s.cancels)
	}
}
