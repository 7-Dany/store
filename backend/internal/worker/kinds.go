package worker

// TODO(jobqueue-phase-6): once internal/platform/jobqueue exists, change the type of
// all constants below from worker.Kind to jobqueue.Kind:
//
//	import "github.com/7-Dany/store/backend/internal/platform/jobqueue"
//	KindPurgeAccounts jobqueue.Kind = "purge_accounts"
//
// Also add KindExecuteRequest, KindSendNotification, and KindPurgeCompleted here
// at that time (per 2-implementation-phases.md Phase 6). The string values must
// not change — they are stored as-is in the job_schedules table.
const (
	// KindPurgeAccounts is the job kind that drives the hourly account purge.
	// Registered with the Dispatcher in server.go during job queue Phase 7;
	// defined here now so no constant needs to be added at that time (D-21).
	// The constant value "purge_accounts" must not change — it is the stable
	// key used by EnsureSchedule to locate the schedule row in the database.
	KindPurgeAccounts Kind = "purge_accounts"
)
