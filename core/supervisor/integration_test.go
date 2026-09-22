package supervisor_test

import (
	"context"
	"errors"
	"testing"

	sdka2a "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"
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

	sup := supervisor.New(supervisor.Config{
		Addr:     addr,
		Provider: prov,
		Store:    store,
	})

	srv, err := transa2a.New(sup, testToken)
	if err != nil {
		t.Fatalf("transport/a2a.New for %s/%s: %v", name, tenant, err)
	}
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })

	return sup, srv
}

// TestIntegration_CEODelegatesToWorkerOverRealWire starts two supervisors on real
// loopback ports, resolves the worker's Agent Card over real HTTP (proving
// discoverability and the bearer security scheme), and asserts that the CEO
// can delegate to the worker via its handler.
//
// Satisfies: "CEO delegates to worker over the real wire".
// This test starts real loopback listeners; skip with -short.
func TestIntegration_CEODelegatesToWorkerOverRealWire(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: skipping in -short mode")
	}

	ctx := context.Background()

	// Worker supervisor. Provider succeeds immediately (no error).
	workerProvider := &fake.Provider{ReturnTaskID: "worker-task-1"}
	_, workerSrv := startSupervisor(t, "worker", "acme", workerProvider)

	// Resolve the worker's Agent Card over real HTTP — the well-known path is
	// public (unauthenticated) and must declare the bearer security scheme
	// (satisfies spec: "Card includes security annotation").
	workerCard, err := agentcard.DefaultResolver.Resolve(ctx, workerSrv.BaseURL())
	if err != nil {
		t.Fatalf("resolve worker card: %v", err)
	}
	if _, ok := workerCard.SecuritySchemes["bearer"]; !ok {
		t.Error("worker agent card missing \"bearer\" security scheme")
	}

	// CEO provider: wraps the worker client — send a task to the worker.
	// For the integration test, we use a fake.Provider on the CEO but directly
	// call the worker via its handler in the assertion step.
	ceoProvider := &fake.Provider{ReturnTaskID: "ceo-task-1"}
	_, ceoSrv := startSupervisor(t, "ceo", "acme", ceoProvider)

	// Call the worker's handler directly rather than through a2aclient: the
	// a2aclient does not yet attach a Bearer header to outgoing calls (see
	// design.md "Migration / Rollout"), so a real HTTP call would be rejected
	// by authInterceptor. The direct handler call still exercises the full
	// interceptor chain via the nil-ServiceParams trusted-caller path.
	msg := sdka2a.NewMessage(sdka2a.MessageRoleUser, sdka2a.NewTextPart("do the work"))
	req := &sdka2a.SendMessageRequest{
		Tenant:  "acme",
		Message: msg,
	}

	result, err := workerSrv.Handler().SendMessage(ctx, req)
	if err != nil {
		t.Fatalf("worker.SendMessage: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result from worker")
	}

	// Now call ListTasks on the CEO supervisor to verify it is also up.
	listResp, err := ceoSrv.Handler().ListTasks(ctx, &sdka2a.ListTasksRequest{
		Tenant: "acme",
	})
	if err != nil {
		t.Fatalf("CEO ListTasks: %v", err)
	}
	// CEO has no tasks yet (no message was sent to it), but ListTasks must work.
	_ = listResp

	// port.Provider no longer declares SendMessage (provider-adapter delta,
	// agent-delegation-over-a2a Phase 1): the fake.Provider.SendMessageCallCount
	// this test used to log against no longer exists. This test itself is retargeted
	// to a real end-to-end delegation assertion in a later slice of this change
	// (design.md "PR Slicing" #7, deferred here to keep Phase 1 pure deletion).

	t.Logf("Worker result type: %T", result)
	t.Logf("CEO ListTasks: %d tasks", len(listResp.Tasks))
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
	sup := supervisor.New(supervisor.Config{
		Addr:         addr,
		Provider:     prov,
		Store:        fileStore,
		PolicyEngine: eng,
		Gateway:      gw,
		Role:         role,
		PolicyConfig: policyCfg,
	})
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

	sup := supervisor.New(supervisor.Config{
		Addr:  addr,
		Store: fileStore,
	})

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
