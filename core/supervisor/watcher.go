package supervisor

import (
	"context"
	"time"

	a2a "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/salgozino/ai-solo-startup-framework/core/port"
)

// defaultPeerPollInterval is how often a parked task's watcher polls its peer
// when Config.PeerPollInterval is unset.
const defaultPeerPollInterval = 2 * time.Second

// resumeInputPeerUpdate is the ResumePoint input a watcher delivers. The resumed
// execution never trusts it: it re-reads the peer's state itself.
const resumeInputPeerUpdate = "peer-update"

// resumeInputPeerTimeout tells the resumed execution that DelegateTimeout elapsed
// while the peer was still running. Like peer-update it is advisory: a premature
// or forged one is harmless because the peer is re-read first, and the worst a
// caller can do with it is fail their own parked task.
const resumeInputPeerTimeout = "peer-wait-timeout"

// watcher is one goroutine polling one parked task's peer.
type watcher struct{ cancel context.CancelFunc }

// SetResumer registers the port used to wake parked tasks. Call before any task
// runs and before RecoverOpenTasks, which re-arms watchers through it.
func (s *Supervisor) SetResumer(r port.TaskResumer) { s.resumer = r }

// parkOnPeer ends the current execution in a parked state and arms the watcher.
//
// The parked A2A state is INPUT_REQUIRED, chosen because it is the one non-terminal
// state a2asrv accepts a later message for: the execution ends (so the task is no
// longer WORKING inside a live execution, which would reject every resume with
// ErrExecutionInProgress and 409 the UI), the stored task stays resumable and
// cancellable, and crash recovery already re-registers INPUT_REQUIRED tasks. It is
// told apart from a human escalation by PendingIntentKind being empty and the
// awaiting-peer fields being set.
func (s *Supervisor) parkOnPeer(
	execCtx *a2asrv.ExecutorContext,
	yield func(a2a.Event, error) bool,
	rec TaskRecord,
	outcome actionOutcome,
	remaining []PendingIntent,
) TaskRecord {
	rec.AwaitingPeerRole = outcome.PeerRole
	rec.AwaitingPeerTaskID = outcome.PeerTaskID
	rec.AwaitingPeerApproval = outcome.PeerApproval
	rec.RemainingIntents = remaining
	s.repark(execCtx, yield, rec)
	return rec
}

// repark persists rec as parked on its peer, yields INPUT_REQUIRED and (re)arms
// the watcher. Used for a first park and when a resume finds the peer still running.
func (s *Supervisor) repark(execCtx *a2asrv.ExecutorContext, yield func(a2a.Event, error) bool, rec TaskRecord) {
	s.log(rec.TaskID).Info("task.parked", "peer_role", rec.AwaitingPeerRole, "peer_task_id", rec.AwaitingPeerTaskID)
	rec.State = string(a2a.TaskStateInputRequired)
	_ = s.cfg.Store.Save(s.cfg.Addr, rec)
	s.notify()
	msg := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("awaiting peer "+rec.AwaitingPeerRole))
	yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateInputRequired, msg), nil) //nolint
	s.armWatcher(rec.TaskID, rec.AwaitingPeerRole, rec.AwaitingPeerTaskID)
}

// armWatcher starts the peer watcher for taskID, replacing any earlier one. After the
// watchers were cancelled it starts nothing: a resume that re-parks concurrently with
// Shutdown or StopWatchers would otherwise arm a watcher the cancel sweep never saw,
// which keeps polling (and blocks StopWatchers' Wait) until DelegateTimeout.
func (s *Supervisor) armWatcher(taskID, role, peerTaskID string) {
	ctx, cancel := context.WithCancel(context.Background())
	w := &watcher{cancel: cancel}

	s.watchMu.Lock()
	if s.watchStopped {
		s.watchMu.Unlock()
		cancel()
		return
	}
	if old := s.watchers[taskID]; old != nil {
		old.cancel()
	}
	s.watchers[taskID] = w
	s.watchWG.Add(1)
	s.watchMu.Unlock()

	go func() {
		defer s.watchWG.Done()
		defer func() {
			s.watchMu.Lock()
			if s.watchers[taskID] == w {
				delete(s.watchers, taskID)
			}
			s.watchMu.Unlock()
		}()
		s.watchPeer(ctx, taskID, role, peerTaskID)
	}()
}

// watchPeer polls the peer and resumes the task through the resumer when the peer
// turns terminal, or when its escalation state differs from what the record says
// (peer escalated, or its human approved and it works again): that resume only
// refreshes the record and re-parks, it never starts a round. An INPUT_REQUIRED
// peer waits on its own human, so nothing times out while it does. A failed poll or
// a failed resume is retried on the next tick; ctx ends the watch. If the peer is
// still running when DelegateTimeout elapses the task is resumed with a timeout
// notice and fails (a re-park or restart re-arms the watcher with a fresh clock).
func (s *Supervisor) watchPeer(ctx context.Context, taskID, role, peerTaskID string) {
	interval := s.cfg.PeerPollInterval
	if interval <= 0 {
		interval = defaultPeerPollInterval
	}
	log := s.log(taskID)
	deadline := time.Now().Add(s.delegateTimeout())
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if s.cfg.Delegator == nil || s.resumer == nil {
			return
		}
		res, err := s.cfg.Delegator.PeerTaskState(ctx, role, peerTaskID)
		if err != nil {
			log.Warn("watch.poll_failed", "error", err)
			continue
		}
		state := a2a.TaskState(res.State)
		input := resumeInputPeerUpdate
		// The task may have left the parked state without this watcher (a verdict
		// consumed the peer result); never resume it, or a human escalation.
		rec, parked := s.parkedRecord(taskID, peerTaskID)
		if !parked {
			return
		}
		escalated := state == a2a.TaskStateInputRequired
		if !state.Terminal() && escalated == rec.AwaitingPeerApproval {
			if escalated || time.Now().Before(deadline) {
				continue
			}
			input = resumeInputPeerTimeout
		}
		// The resumed execution has its own lifecycle (it may run a whole provider
		// round, or re-park and replace this watcher), so it must not die with this
		// watcher's ctx; cancellation goes through Cancel, which stops the task.
		if err := s.resumer.Resume(context.WithoutCancel(ctx), port.ResumePoint{TaskID: taskID, Input: input}); err != nil {
			log.Warn("watch.resume_failed", "error", err)
			continue
		}
		return
	}
}

// parkedRecord returns taskID's record and whether it is still parked on
// peerTaskID rather than escalated to a human, finished or gone.
func (s *Supervisor) parkedRecord(taskID, peerTaskID string) (TaskRecord, bool) {
	rec, err := s.cfg.Store.Load(s.cfg.Addr, taskID)
	return rec, err == nil && rec.State == string(a2a.TaskStateInputRequired) &&
		rec.AwaitingPeerTaskID == peerTaskID && rec.PendingIntentKind == ""
}

// stopWatcher cancels the watcher for taskID, if any, without waiting for it;
// idempotent, and safe to call from the watcher itself.
func (s *Supervisor) stopWatcher(taskID string) {
	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	if w := s.watchers[taskID]; w != nil {
		w.cancel()
		delete(s.watchers, taskID)
	}
}

// cancelWatchers cancels every watcher without waiting for them.
func (s *Supervisor) cancelWatchers() {
	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	s.watchStopped = true
	for id, w := range s.watchers {
		w.cancel()
		delete(s.watchers, id)
	}
}

// StopWatchers cancels every watcher and waits for the goroutines to exit.
func (s *Supervisor) StopWatchers() {
	s.cancelWatchers()
	s.watchWG.Wait()
}

func (s *Supervisor) watcherCount() int {
	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	return len(s.watchers)
}
