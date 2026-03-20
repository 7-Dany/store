package telemetry

import (
	"context"
	"errors"
	"net"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Layer identifies the architectural layer where an error originated.
// All values are valid Prometheus label values: lowercase, underscores, bounded set.
type Layer string

const (
	LayerStore   Layer = "store"   // domain/*/store.go — DB access
	LayerService Layer = "service" // domain/*/service.go — business logic
	LayerHandler Layer = "handler" // domain/*/handler.go — marshal / mint errors
	LayerOAuth   Layer = "oauth"   // domain/oauth outbound HTTP to providers
	LayerMailer  Layer = "mailer"  // platform/mailer — SMTP
	LayerToken   Layer = "token"   // platform/token — JWT sign/parse
	LayerCrypto  Layer = "crypto"  // platform/crypto — AES-GCM
	LayerKVStore Layer = "kvstore" // platform/kvstore — Redis ops
	LayerRBAC    Layer = "rbac"    // platform/rbac — DB permission checks
	LayerWorker  Layer = "worker"  // jobqueue dispatcher infrastructure errors
	LayerPanic   Layer = "panic"   // recovered HTTP handler panic
	LayerUnknown Layer = "unknown" // no *Fault in chain — unmigrated path
)

// Cause classifies the root cause of an error for Prometheus labelling.
// All values are valid Prometheus label values: lowercase, underscores, bounded set.
type Cause string

const (
	CauseDB           Cause = "db_error"
	CauseDBConstraint Cause = "db_constraint"    // SQLSTATE 23505/23503/23514/23502
	CauseDBPool       Cause = "db_pool"          // pool exhausted — SQLSTATE 53300, 08xxx
	CauseDBTimeout    Cause = "db_timeout"       // SQLSTATE 57014 query cancelled
	CauseTimeout      Cause = "timeout"          // context.DeadlineExceeded or net timeout
	CauseClientGone   Cause = "client_cancelled" // context.Canceled (client disconnect)
	CauseNetwork      Cause = "network_error"    // non-timeout net.Error
	CausePanic        Cause = "panic"            // recovered HTTP handler panics
	CauseUnknown      Cause = "unknown"
)

// Fault wraps an error with layer and operation metadata.
// It satisfies the error interface and supports errors.Is/As unwrapping.
//
// Use the layer-specific constructors ([Store], [Service], [Handler], etc.)
// rather than constructing Fault directly.
type Fault struct {
	// Op is the operation label: "TypeName.step". Appears in logs only,
	// never in Prometheus labels (unbounded cardinality risk).
	Op    string
	Layer Layer
	Err   error
}

// Error returns a human-readable representation with layer, op, and underlying message.
func (f *Fault) Error() string {
	if f.Op != "" {
		return string(f.Layer) + "." + f.Op + ": " + f.Err.Error()
	}
	return string(f.Layer) + ": " + f.Err.Error()
}

// Unwrap allows errors.Is/As to traverse through the Fault chain.
func (f *Fault) Unwrap() error { return f.Err }

// wrap creates a *Fault. Returns nil when err is nil — safe in return statements.
func wrap(layer Layer, op string, err error) error {
	if err == nil {
		return nil
	}
	return &Fault{Op: op, Layer: layer, Err: err}
}

// Store wraps err with [LayerStore] and the given operation label.
// Returns nil when err is nil.
//
// Op naming convention: "TypeName.step" e.g. "GetUserForLogin.query", "LoginTx.begin_tx".
func Store(op string, err error) error { return wrap(LayerStore, op, err) }

// Service wraps err with [LayerService] and the given operation label.
// Returns nil when err is nil.
func Service(op string, err error) error { return wrap(LayerService, op, err) }

// Handler wraps err with [LayerHandler] and the given operation label.
// Returns nil when err is nil.
func Handler(op string, err error) error { return wrap(LayerHandler, op, err) }

// OAuth wraps err with [LayerOAuth] and the given operation label.
// Returns nil when err is nil.
func OAuth(op string, err error) error { return wrap(LayerOAuth, op, err) }

// Mailer wraps err with [LayerMailer] and the given operation label.
// Returns nil when err is nil.
func Mailer(op string, err error) error { return wrap(LayerMailer, op, err) }

// Token wraps err with [LayerToken] and the given operation label.
// Returns nil when err is nil.
func Token(op string, err error) error { return wrap(LayerToken, op, err) }

// Crypto wraps err with [LayerCrypto] and the given operation label.
// Returns nil when err is nil.
func Crypto(op string, err error) error { return wrap(LayerCrypto, op, err) }

// KVStore wraps err with [LayerKVStore] and the given operation label.
// Returns nil when err is nil.
func KVStore(op string, err error) error { return wrap(LayerKVStore, op, err) }

// RBAC wraps err with [LayerRBAC] and the given operation label.
// Returns nil when err is nil.
func RBAC(op string, err error) error { return wrap(LayerRBAC, op, err) }

// Worker wraps err with [LayerWorker] and the given operation label.
// Returns nil when err is nil.
func Worker(op string, err error) error { return wrap(LayerWorker, op, err) }

// LayerOf returns the Layer of the outermost *Fault in the error chain.
// "Outermost" means closest to the call site:
//
//	Service("op", Store("op2", err)) → LayerService
//
// Returns [LayerUnknown] when no *Fault is present (unmigrated call site).
func LayerOf(err error) Layer {
	var f *Fault
	if errors.As(err, &f) {
		return f.Layer
	}
	return LayerUnknown
}

// OpOf returns the Op of the outermost *Fault in the error chain.
// Returns "" when no *Fault is present.
// Used for the fault_op log attribute only — never a Prometheus label.
func OpOf(err error) string {
	var f *Fault
	if errors.As(err, &f) {
		return f.Op
	}
	return ""
}

// FaultEntry is a single entry in a fault chain, used for the fault_chain log attribute.
type FaultEntry struct {
	Layer string `json:"layer"`
	Op    string `json:"op,omitempty"`
}

// FaultChain returns all *Fault values in the chain, outermost-first.
// Used to build the fault_chain structured log attribute.
func FaultChain(err error) []FaultEntry {
	var chain []FaultEntry
	for cur := err; cur != nil; {
		var f *Fault
		if !errors.As(cur, &f) {
			break
		}
		chain = append(chain, FaultEntry{Layer: string(f.Layer), Op: f.Op})
		cur = f.Err
	}
	return chain
}

// ClassifyCause inspects the error chain and returns the best matching [Cause].
// It traverses through *Fault wrappers to find the root cause.
//
//	ClassifyCause(Service("op", pgErr)) → CauseDBConstraint
func ClassifyCause(err error) Cause {
	if err == nil {
		return ""
	}

	// Check for a panic Fault first.
	// PanicRecoveryMiddleware wraps panics as &Fault{Layer: LayerPanic, ...}.
	// The inner error is a plain fmt.Errorf that matches nothing below —
	// without this guard ClassifyCause would return CauseUnknown for panics.
	var f *Fault
	if errors.As(err, &f) && f.Layer == LayerPanic {
		return CausePanic
	}

	// pgconn.PgError carries a SQLSTATE code — most precise classification.
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return classifyPgCode(pgErr.Code)
	}

	// pgx.ErrNoRows — defence-in-depth; stores should convert this to sentinels
	// before returning, but classify defensively in case they don't.
	if errors.Is(err, pgx.ErrNoRows) {
		return CauseDB
	}

	// Check context errors before net.Error — context.DeadlineExceeded also
	// satisfies net.Error.Timeout() in some wrapping scenarios.
	if errors.Is(err, context.DeadlineExceeded) {
		return CauseTimeout
	}
	if errors.Is(err, context.Canceled) {
		return CauseClientGone
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return CauseTimeout
		}
		return CauseNetwork
	}

	return CauseUnknown
}

func classifyPgCode(code string) Cause {
	switch code {
	case "23505", "23503", "23514", "23502":
		return CauseDBConstraint
	case "53300", "08006", "08001", "08004":
		// 53300 = too many connections, 08xxx = connection failure codes
		return CauseDBPool
	case "57014":
		// query_canceled
		return CauseDBTimeout
	default:
		return CauseDB
	}
}
