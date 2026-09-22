package supervisor_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	sdka2a "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv/taskstore"

	"github.com/salgozino/ai-solo-startup-framework/config"
	"github.com/salgozino/ai-solo-startup-framework/core/address"
	"github.com/salgozino/ai-solo-startup-framework/core/policy"
	"github.com/salgozino/ai-solo-startup-framework/core/port"
	"github.com/salgozino/ai-solo-startup-framework/core/port/fake"
	"github.com/salgozino/ai-solo-startup-framework/core/supervisor"
	transa2a "github.com/salgozino/ai-solo-startup-framework/transport/a2a"
)

const testToken = "test-bearer-token"

// startSupervisor starts a supervisor on a random loopback port and returns it
// along with its transport server. Cleanup is registered automatically.
func startSupervisor(t *testing.T, name, tenant string, prov *fake.Provider) (*supervisor.Supervisor, *transa2a.Server) {
	t.Helper()

	addr, err := address.New(name, tenant)
	if err != nil {
		t.Fatalf("address.New(%q,%q): %v", name, tenant, err)
	}

	store, err := supervisor.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	sup, err := supervisor.New(supervisor.Config{
		Addr:         addr,
		Provider:     prov,
		Store:        store,
		PolicyEngine: policy.NewEngine(),
	})
	if err != nil {
		t.Fatalf("supervisor.New: %v", err)
	}

	srv, err := transa2a.New(sup, testToken)
	if err != nil {
		t.Fatalf("transport/a2a.New for %s/%s: %v", name, tenant, err)
	}
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })

	return sup, srv
}

// startDelegatingSupervisor starts a supervisor configured for the Phase 7
// real-wire delegation tests: a Delegator, a DelegateTimeout, and a full
// PolicyConfig, on top of startSupervisor's plain construction. Cleanup is
// registered automatically.
func startDelegatingSupervisor(
	t *testing.T,
	role, tenant string,
	prov port.Provider,
	delegator port.Delegator,
	delegateTimeout time.Duration,
	policyCfg map[string]config.Policy,
) (*supervisor.Supervisor, *transa2a.Server) {
	t.Helper()

	addr, err := address.New(role, tenant)
	if err != nil {
		t.Fatalf("address.New(%q,%q): %v", role, tenant, err)
	}

	store, err := supervisor.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	sup, err := supervisor.New(supervisor.Config{
		Addr:            addr,
		Provider:        prov,
		Store:           store,
		PolicyEngine:    policy.NewEngine(),
		Gateway:         &fake.Gateway{},
		Delegator:       delegator,
		DelegateTimeout: delegateTimeout,
		Role:            role,
		PolicyConfig:    policyCfg,
	})
	if err != nil {
		t.Fatalf("supervisor.New: %v", err)
	}

	srv, err := transa2a.New(sup, testToken)
	if err != nil {
		t.Fatalf("transport/a2a.New for %s/%s: %v", role, tenant, err)
	}
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })

	return sup, srv
}

// delegationCompany bundles a real CEO and engineer supervisor pair, both
// declared in one shared PeerDirectory, with the CEO's Delegator wired to a
// real transport/a2a.Client addressing the engineer by role. Built for the
// Phase 7 real-wire delegation tests (tasks 7.9-7.12).
type delegationCompany struct {
	delegator   *transa2a.Client
	ceoSrv      *transa2a.Server
	engineerSup *supervisor.Supervisor
}

// startDelegationCompany wires ceoProv and engineerProv into a real CEO/engineer
// pair over the real A2A wire: real transa2a.Server instances, a real
// transport/a2a.Client, a real PeerDirectory, and real supervisor routing —
// only the CLI subprocess is faked, per repo convention. delegateTimeout of 0
// uses the supervisor's own 10-minute default.
func startDelegationCompany(t *testing.T, tenant string, ceoProv, engineerProv port.Provider, delegateTimeout time.Duration) *delegationCompany {
	t.Helper()
	if testing.Short() {
		t.Skip("integration: skipping in -short mode")
	}

	dir, err := transa2a.NewPeerDirectory([]string{"ceo", "engineer"})
	if err != nil {
		t.Fatalf("NewPeerDirectory: %v", err)
	}

	engineerSup, engineerSrv := startDelegatingSupervisor(t, "engineer", tenant, engineerProv, nil, 0, map[string]config.Policy{})
	if err := dir.Bind("engineer", engineerSrv.BaseURL()); err != nil {
		t.Fatalf("dir.Bind(engineer): %v", err)
	}

	delegator := transa2a.NewClient(dir, tenant, testToken, nil)
	policyCfg := map[string]config.Policy{
		port.KindDelegateTask: {Risk: "safe", AllowedRoles: []string{"ceo"}},
	}
	_, ceoSrv := startDelegatingSupervisor(t, "ceo", tenant, ceoProv, delegator, delegateTimeout, policyCfg)
	if err := dir.Bind("ceo", ceoSrv.BaseURL()); err != nil {
		t.Fatalf("dir.Bind(ceo): %v", err)
	}

	return &delegationCompany{delegator: delegator, ceoSrv: ceoSrv, engineerSup: engineerSup}
}

// delegateTaskIntentTo builds a delegate_task ActionIntent with the {target,
// body} payload shape, addressed to role.
func delegateTaskIntentTo(role, body string) port.ActionIntent {
	return port.ActionIntent{
		Kind:    port.KindDelegateTask,
		Payload: map[string]any{port.TargetArg: role, "body": body},
	}
}

// sendCEOTask drives one new task through the CEO's real A2A handler — the
// same entry point a real CLI-backed agent's completed turn would use — and
// returns the terminal *a2a.Task.
func sendCEOTask(t *testing.T, ceoSrv *transa2a.Server, tenant, input string) *sdka2a.Task {
	t.Helper()
	msg := sdka2a.NewMessage(sdka2a.MessageRoleUser, sdka2a.NewTextPart(input))
	result, err := ceoSrv.Handler().SendMessage(context.Background(), &sdka2a.SendMessageRequest{
		Tenant:  tenant,
		Message: msg,
	})
	if err != nil {
		t.Fatalf("CEO SendMessage: %v", err)
	}
	task, ok := result.(*sdka2a.Task)
	if !ok {
		t.Fatalf("CEO SendMessage: result type = %T, want *a2a.Task", result)
	}
	return task
}

// textOf returns the first text part of msg, or "" if msg is nil or carries none.
func textOf(msg *sdka2a.Message) string {
	if msg == nil {
		return ""
	}
	for _, part := range msg.Parts {
		if text := part.Text(); text != "" {
			return text
		}
	}
	return ""
}

// TestIntegration_CEODelegatesToEngineerOverRealWire replaces the stale
// TestIntegration_CEODelegatesToWorkerOverRealWire, which called the worker's
// handler directly and tolerated zero delegation calls. This test drives a
// real delegate_task intent through the CEO's own A2A handler, over the real
// wire, to a real engineer supervisor, and asserts the CEO's task carries the
// engineer's terminal output — the proposal's first Success Criterion.
//
// Falsification (task 7.9): this test passes unmodified against the current
// tree because Phases 4-6 already implement the client, the output-on-the-wire
// change, and the supervisor's delegation routing. To prove it actually
// exercises that real path rather than passing vacuously, executeAction's
// delegate_task branch was temporarily commented out (falling through to the
// nil-Gateway error path): the test then failed with "CEO task state = FAILED,
// want COMPLETED" and engineerProv.RunTaskCallCount() == 0, for exactly the
// reason expected. The branch was restored before this file was committed.
//
// Satisfies: agent-delegation "A completed peer delegation completes the
// delegating task with the peer's output"; tasks 7.9/7.10.
func TestIntegration_CEODelegatesToEngineerOverRealWire(t *testing.T) {
	tenant := "acme"
	engineerProv := &fake.Provider{ReturnRunResult: port.ProviderResult{Output: "Done: implemented X"}}
	ceoProv := &fake.Provider{ReturnRunResult: port.ProviderResult{
		ActionIntents: []port.ActionIntent{delegateTaskIntentTo("engineer", "build X")},
	}}
	co := startDelegationCompany(t, tenant, ceoProv, engineerProv, 0)

	task := sendCEOTask(t, co.ceoSrv, tenant, "please delegate")

	if task.Status.State != sdka2a.TaskStateCompleted {
		t.Fatalf("CEO task state = %v, want COMPLETED", task.Status.State)
	}
	if engineerProv.RunTaskCallCount() == 0 {
		t.Error("expected the engineer's provider to run at least once")
	}
	if got := textOf(task.Status.Message); !strings.Contains(got, "Done: implemented X") {
		t.Errorf("CEO task output = %q, want it to contain the engineer's output", got)
	}
}

// TestIntegration_DelegatingCLIInvokedExactlyOnce proves the delegating agent's
// own provider (its CLI subprocess) is invoked exactly once for a delegated
// task — never a second time to "receive" or "react to" the peer's output, as
// the delegating agent's own turn already ended before the peer answered.
// Satisfies: agent-delegation "No second invocation of the delegating agent's
// CLI occurs"; task 7.11.
func TestIntegration_DelegatingCLIInvokedExactlyOnce(t *testing.T) {
	tenant := "acme"
	engineerProv := &fake.Provider{ReturnRunResult: port.ProviderResult{Output: "Done: implemented X"}}
	ceoProv := &fake.Provider{ReturnRunResult: port.ProviderResult{
		ActionIntents: []port.ActionIntent{delegateTaskIntentTo("engineer", "build X")},
	}}
	co := startDelegationCompany(t, tenant, ceoProv, engineerProv, 0)

	task := sendCEOTask(t, co.ceoSrv, tenant, "please delegate")
	if task.Status.State != sdka2a.TaskStateCompleted {
		t.Fatalf("CEO task state = %v, want COMPLETED", task.Status.State)
	}

	if got := ceoProv.RunTaskCallCount(); got != 1 {
		t.Errorf("CEO provider RunTask call count = %d, want exactly 1", got)
	}
}

// blockingUntilReleased is a port.Provider whose RunTask blocks until release
// is closed, simulating a peer that is still actively working. It ignores ctx
// entirely: the point of this test double is to prove the delegator's own
// timeout does NOT propagate into the peer's execution as a side effect —
// wiring it to ctx.Done() would defeat that proof.
type blockingUntilReleased struct {
	*fake.Provider
	release chan struct{}
}

func (p *blockingUntilReleased) RunTask(ctx context.Context, taskID, input string) (port.ProviderResult, error) {
	<-p.release
	return p.Provider.RunTask(ctx, taskID, input)
}

var _ port.Provider = (*blockingUntilReleased)(nil)

// TestIntegration_DelegationTimeoutLeavesPeerRunning proves that when the
// CEO's delegation times out, the engineer's own task is left running under
// its own supervisor — never canceled, rejected, or altered as a side effect.
// Satisfies: agent-delegation "The peer's task is not canceled on the
// delegator's timeout"; task 7.12.
func TestIntegration_DelegationTimeoutLeavesPeerRunning(t *testing.T) {
	tenant := "acme"
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	engineerProv := &blockingUntilReleased{
		Provider: &fake.Provider{ReturnRunResult: port.ProviderResult{Output: "eventually done"}},
		release:  release,
	}
	ceoProv := &fake.Provider{ReturnRunResult: port.ProviderResult{
		ActionIntents: []port.ActionIntent{delegateTaskIntentTo("engineer", "build X")},
	}}
	const delegateTimeout = 100 * time.Millisecond
	co := startDelegationCompany(t, tenant, ceoProv, engineerProv, delegateTimeout)

	task := sendCEOTask(t, co.ceoSrv, tenant, "please delegate")

	if task.Status.State != sdka2a.TaskStateFailed {
		t.Fatalf("CEO task state = %v, want FAILED (timeout)", task.Status.State)
	}

	// The engineer's task is still mid-flight (RunTask is blocked on release).
	// Find its task ID directly from the engineer's own store — no wire call
	// needed to discover the ID the framework assigned it.
	recs, err := co.engineerSup.ListTasks()
	if err != nil {
		t.Fatalf("engineer ListTasks: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("expected exactly 1 engineer task record, got %d", len(recs))
	}
	peerTaskID := recs[0].TaskID

	res, err := co.delegator.PeerTaskState(context.Background(), "engineer", peerTaskID)
	if err != nil {
		t.Fatalf("PeerTaskState: %v", err)
	}
	if res.State != string(sdka2a.TaskStateWorking) {
		t.Errorf("engineer peer task state = %q, want %q (still running, not canceled)", res.State, sdka2a.TaskStateWorking)
	}
}

// TestIntegration_InputRequiredRecoveredAndResumed verifies that an INPUT_REQUIRED task
// persisted in the file store before a restart is:
//  1. Registered in the a2asrv in-memory store by RecoverOpenTasks (via registerFn).
//  2. Recognisable as a resume (ExecutorContext.StoredTask != nil) when a subsequent
//     SendMessage carries the same TaskID.
//  3. Handled via executeResume — RunTask is NOT called again.
//  4. Completed with a single gateway call when the approval arrives.
//
// Satisfies: Bug 3 spec "INPUT_REQUIRED task still resumable after restart".
// This test starts real loopback listeners; skip with -short.
func TestIntegration_InputRequiredRecoveredAndResumed(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: skipping in -short mode")
	}

	ctx := context.Background()

	const (
		taskID = "seeded-ir-task-1"
		tenant = "acme"
		role   = "ceo"
	)

	addr, err := address.New(role, tenant)
	if err != nil {
		t.Fatalf("address.New: %v", err)
	}

	prov := &fake.Provider{
		ReturnRunResult: port.ProviderResult{
			ActionIntents: []port.ActionIntent{{Kind: "telegram_send"}},
		},
	}
	gw := &fake.Gateway{}
	eng := policy.NewEngine()
	policyCfg := map[string]config.Policy{
		"telegram_send": {Risk: "risky", AllowedRoles: []string{role}},
	}

	// Shared file store — persists across the simulated restart.
	fileStore, err := supervisor.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	// Pre-seed the file store with a task parked in INPUT_REQUIRED, simulating
	// a supervisor that escalated the task and then crashed before the human replied.
	if err := fileStore.Save(addr, supervisor.TaskRecord{
		TaskID:            taskID,
		State:             string(sdka2a.TaskStateInputRequired),
		Input:             "send a telegram",
		Owner:             string(addr),
		PendingIntentKind: "telegram_send",
	}); err != nil {
		t.Fatalf("store.Save (seed): %v", err)
	}

	// Create supervisor — transa2a.New calls RecoverOpenTasks which invokes registerFn
	// for the INPUT_REQUIRED task, seeding it into the a2asrv in-memory store.
	sup, err := supervisor.New(supervisor.Config{
		Addr:         addr,
		Provider:     prov,
		Store:        fileStore,
		PolicyEngine: eng,
		Gateway:      gw,
		Role:         role,
		PolicyConfig: policyCfg,
	})
	if err != nil {
		t.Fatalf("supervisor.New: %v", err)
	}
	srv, err := transa2a.New(sup, testToken)
	if err != nil {
		t.Fatalf("transa2a.New: %v", err)
	}
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })

	runCountBefore := prov.RunTaskCallCount()

	// Send an approval message carrying the original TaskID.
	// The a2asrv framework finds the task in its store → StoredTask != nil → resume path.
	approval := sdka2a.NewMessage(sdka2a.MessageRoleUser, sdka2a.NewTextPart("approve"))
	approval.TaskID = sdka2a.TaskID(taskID)
	_, err = srv.Handler().SendMessage(ctx, &sdka2a.SendMessageRequest{
		Tenant:  tenant,
		Message: approval,
	})
	if err != nil {
		t.Logf("approval SendMessage: %v (non-fatal; checking assertions)", err)
	}

	// Resume must NOT call RunTask (it's a resume, not a fresh Execute).
	if prov.RunTaskCallCount() != runCountBefore {
		t.Errorf("resume called RunTask; before=%d after=%d",
			runCountBefore, prov.RunTaskCallCount())
	}

	// Gateway must be called exactly once after the human approved.
	if gw.CallCount() != 1 {
		t.Errorf("expected gateway.Send called once on approval; got %d", gw.CallCount())
	}
}

// TestRecoverOpenTasks_DoubleRecoveryIsIdempotent verifies that calling RecoverOpenTasks
// twice with the same INPUT_REQUIRED data does not return an error.
// The first call seeds the a2asrv in-memory task store; the second call encounters
// taskstore.ErrTaskAlreadyExists which the registerFn suppresses, making it idempotent.
//
// Satisfies: Bug 3 spec "Double-recovery is idempotent".
func TestRecoverOpenTasks_DoubleRecoveryIsIdempotent(t *testing.T) {
	ctx := context.Background()

	const (
		taskID = "idempotent-ir-task-1"
		tenant = "acme"
		role   = "ceo"
	)

	addr, err := address.New(role, tenant)
	if err != nil {
		t.Fatalf("address.New: %v", err)
	}

	fileStore, err := supervisor.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	// Pre-seed an INPUT_REQUIRED task, simulating a supervisor that parked a task
	// awaiting human approval before it was shut down.
	if err := fileStore.Save(addr, supervisor.TaskRecord{
		TaskID:            taskID,
		State:             string(sdka2a.TaskStateInputRequired),
		Input:             "send a telegram",
		Owner:             string(addr),
		PendingIntentKind: "telegram_send",
	}); err != nil {
		t.Fatalf("store.Save (seed): %v", err)
	}

	sup, err := supervisor.New(supervisor.Config{
		Addr:         addr,
		Store:        fileStore,
		PolicyEngine: policy.NewEngine(),
	})
	if err != nil {
		t.Fatalf("supervisor.New: %v", err)
	}

	// Build a registerFn backed by a real in-memory task store, mirroring what
	// transport/a2a.New does: ErrTaskAlreadyExists is suppressed (idempotent).
	memStore := taskstore.NewInMemory(&taskstore.InMemoryStoreConfig{
		Authenticator: func(_ context.Context) (string, error) {
			return "supervisor", nil
		},
	})
	registerFn := func(ctx context.Context, taskID, _ string) error {
		task := &sdka2a.Task{
			ID:     sdka2a.TaskID(taskID),
			Status: sdka2a.TaskStatus{State: sdka2a.TaskStateInputRequired},
		}
		_, err := memStore.Create(ctx, task)
		if errors.Is(err, taskstore.ErrTaskAlreadyExists) {
			return nil // idempotent: already registered
		}
		return err
	}

	// First recovery: seeds the in-memory store — must succeed.
	if err := sup.RecoverOpenTasks(ctx, nil, registerFn); err != nil {
		t.Fatalf("first RecoverOpenTasks: %v", err)
	}

	// Second recovery on the same data: ErrTaskAlreadyExists is suppressed — must also succeed.
	if err := sup.RecoverOpenTasks(ctx, nil, registerFn); err != nil {
		t.Fatalf("second RecoverOpenTasks (must be idempotent): %v", err)
	}
}
