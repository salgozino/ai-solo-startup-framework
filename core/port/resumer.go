package port

import "context"

// TaskResumer re-enters a parked task. A task waiting on a peer ends its A2A
// execution in a resumable, non-terminal state; when the wait is over the
// supervisor asks the resumer to deliver a resume for it, exactly as if a message
// carrying the task's ID had arrived over the wire. Implementations live in
// transport/a2a (the a2asrv handler); core/ never imports the transport.
type TaskResumer interface {
	// Resume delivers rp to the parked task rp.TaskID. It blocks until the resumed
	// execution stops advancing (completes, fails, or parks again). A returned
	// error means the resume was not delivered and may be retried.
	Resume(ctx context.Context, rp ResumePoint) error
}
