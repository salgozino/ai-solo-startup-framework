package supervisor

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/salgozino/ai-solo-startup-framework/core/address"
	"github.com/salgozino/ai-solo-startup-framework/core/port"
)

// Store.Save rewrites the ENTIRE per-agent task array on every call
// (load -> unmarshal -> replace one element -> marshal -> write -> rename), and
// the supervisor calls it 8+ times per task. Putting the transcript on
// TaskRecord therefore makes each save O(tasks x transcript bytes), not O(1).
// These benchmarks measure that cost before any slice depends on the layout.
//
// The shape is deliberately realistic rather than adversarial:
//   - benchStoreTasks tasks live in one agent's file (a day of work, not a stress test)
//   - each carries benchTurnsPerTask turns of benchTurnBytes each
//
// Every task carries a transcript because that is the steady state of a running
// company: tasks accumulate rounds and are not pruned between saves.
//
// The store size is a benchmark axis rather than a fixed number because
// Store.Delete has exactly one production caller (Cancel, supervisor.go:583).
// Completed tasks are never pruned, so the per-agent file grows for the life of
// the process and the scaling curve — not the 20-task point — is what decides
// the storage layout.
const (
	benchStoreTasks   = 20
	benchTurnsPerTask = 10
	benchTurnBytes    = 4096
)

// benchStoreSizes are the task counts the scaling benchmarks sweep.
// benchStoreTasks is the realistic point; the others show the growth curve.
var benchStoreSizes = []int{benchStoreTasks, 100, 500}

// benchTurnContent builds a ~benchTurnBytes string that is not a single repeated
// rune, so JSON escaping and allocation behave like real agent output rather
// than like a best case.
func benchTurnContent(seed int) string {
	var b strings.Builder
	b.Grow(benchTurnBytes + 64)
	line := fmt.Sprintf("round %d: the engineer reports progress on the migration plan and lists open questions. ", seed)
	for b.Len() < benchTurnBytes {
		b.WriteString(line)
	}
	return b.String()[:benchTurnBytes]
}

// benchTurns returns a realistic transcript for one task.
func benchTurns() []port.ContextMessage {
	base := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	turns := make([]port.ContextMessage, 0, benchTurnsPerTask)
	for i := range benchTurnsPerTask {
		role := "assistant"
		if i%2 == 1 {
			role = "peer"
		}
		turns = append(turns, port.ContextMessage{
			Role:    role,
			Content: benchTurnContent(i),
			At:      base.Add(time.Duration(i) * time.Minute),
		})
	}
	return turns
}

// seedBenchStore fills one agent file with tasks records, optionally each
// carrying a full transcript, and returns the store plus the address.
func seedBenchStore(b *testing.B, tasks int, withTranscript bool) (*Store, address.A2AAddress) {
	b.Helper()
	s, err := NewStore(b.TempDir())
	if err != nil {
		b.Fatalf("NewStore: %v", err)
	}
	addr, err := address.New("ceo", "acme")
	if err != nil {
		b.Fatalf("address.New: %v", err)
	}
	for i := range tasks {
		rec := TaskRecord{
			TaskID: fmt.Sprintf("task-%04d", i),
			State:  "TASK_STATE_WORKING",
			Input:  "plan the migration",
			Owner:  string(addr),
		}
		if withTranscript {
			rec.Turns = benchTurns()
		}
		if err := s.Save(addr, rec); err != nil {
			b.Fatalf("seed Save: %v", err)
		}
	}
	return s, addr
}

// benchSave runs the Save loop against a seeded store and reports the resulting
// on-disk file size, which is the quantity the cost is linear in.
func benchSave(b *testing.B, tasks int, withTranscript bool) {
	b.Helper()
	s, addr := seedBenchStore(b, tasks, withTranscript)
	rec := TaskRecord{
		TaskID: fmt.Sprintf("task-%04d", tasks/2),
		State:  "TASK_STATE_WORKING",
		Input:  "plan the migration",
		Owner:  string(addr),
	}
	if withTranscript {
		rec.Turns = benchTurns()
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := s.Save(addr, rec); err != nil {
			b.Fatalf("Save: %v", err)
		}
	}
	b.StopTimer()
	// Reported after the loop on purpose: ResetTimer clears the extra-metric map,
	// so a ReportMetric call placed before it is silently discarded.
	info, err := os.Stat(s.path(addr))
	if err != nil {
		b.Fatalf("stat store file: %v", err)
	}
	b.ReportMetric(float64(info.Size())/1024, "KiB-file")
}

// BenchmarkStoreSave_WithoutTranscript is the control: today's flat record shape
// at each store size. It exists so the transcript cost is a delta, not an
// unanchored number.
func BenchmarkStoreSave_WithoutTranscript(b *testing.B) {
	for _, tasks := range benchStoreSizes {
		b.Run(fmt.Sprintf("tasks=%d", tasks), func(b *testing.B) {
			benchSave(b, tasks, false)
		})
	}
}

// BenchmarkStoreSave_WithTranscript measures one Save of a record carrying a
// realistic transcript, into a store where every other task carries one too.
// This is the number that decides whether Turns can stay on TaskRecord.
func BenchmarkStoreSave_WithTranscript(b *testing.B) {
	for _, tasks := range benchStoreSizes {
		b.Run(fmt.Sprintf("tasks=%d", tasks), func(b *testing.B) {
			benchSave(b, tasks, true)
		})
	}
}
