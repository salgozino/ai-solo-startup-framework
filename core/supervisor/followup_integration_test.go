package supervisor_test

import (
	"context"
	"strings"
	"testing"

	sdka2a "github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/salgozino/ai-solo-startup-framework/core/port"
	"github.com/salgozino/ai-solo-startup-framework/core/port/fake"
)

// S2 over the real a2asrv handler: a follow-up message that references a
// completed task and reuses its context is accepted as a new task in that
// context, and the followed task stays completed.
func TestFollowUp_OverRealWire(t *testing.T) {
	prov := &fake.Provider{ReturnRunResult: port.ProviderResult{Output: "draft v1"}}
	sup, srv := startSupervisor(t, "ceo", "acme", prov)
	sup.MarkReady()

	first := sendCEOTask(t, srv, "acme", "write a draft")
	if first.Status.State != sdka2a.TaskStateCompleted {
		t.Fatalf("first task = %s, want COMPLETED", first.Status.State)
	}

	prov.ReturnRunResult = port.ProviderResult{Output: "draft v2"}
	msg := sdka2a.NewMessage(sdka2a.MessageRoleUser, sdka2a.NewTextPart("I don't like it, make it longer"))
	msg.ReferenceTasks = []sdka2a.TaskID{first.ID}
	msg.ContextID = first.ContextID
	res, err := srv.Handler().SendMessage(context.Background(), &sdka2a.SendMessageRequest{Tenant: "acme", Message: msg})
	if err != nil {
		t.Fatalf("follow-up SendMessage: %v", err)
	}
	second, ok := res.(*sdka2a.Task)
	if !ok {
		t.Fatalf("follow-up result = %T, want *a2a.Task", res)
	}
	if second.ID == first.ID || second.ContextID != first.ContextID || second.Status.State != sdka2a.TaskStateCompleted {
		t.Fatalf("follow-up = id %s ctx %s state %s; first = id %s ctx %s", second.ID, second.ContextID, second.Status.State, first.ID, first.ContextID)
	}
	recs, err := sup.ListTasks()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recs {
		switch sdka2a.TaskID(r.TaskID) {
		case first.ID:
			if r.State != string(sdka2a.TaskStateCompleted) || r.Output != "draft v1" || r.FollowUpOf != "" {
				t.Errorf("followed task changed: %+v", r)
			}
		case second.ID:
			if r.FollowUpOf != string(first.ID) || r.ContextID != first.ContextID || r.Output != "draft v2" {
				t.Errorf("follow-up record = %+v", r)
			}
		}
	}
	if got := prov.RunCalls[1].Input; !strings.Contains(got, "draft v1") || !strings.Contains(got, "make it longer") {
		t.Errorf("follow-up input lacks the chain: %q", got)
	}
}
