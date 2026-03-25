package events

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/7-Dany/store/backend/internal/platform/bitcoin/rpc"
	"github.com/7-Dany/store/backend/internal/platform/bitcoin/zmq"
)

// ── mempoolRecorder ───────────────────────────────────────────────────────────

// mempoolRecorder is the narrow metric interface MempoolTracker needs.
// Satisfied by *telemetry.Registry and NoopBitcoinRecorder; also nil-safe
// (all calls are guarded by a nil check inside the methods below).
type mempoolRecorder interface {
	SetPendingMempoolSize(n int)
	OnMempoolEntryDropped(reason string)
	OnRBFDetected()
	OnMempoolPruned(count int)
}

// pendingEntry is one in-mempool transaction tracked for SSE display.
type pendingEntry struct {
	// addrs is the set of output addresses matched to at least one watch set.
	addrs map[string]struct{}
	// addedAt is when this entry entered pendingMempool (for age pruning).
	addedAt time.Time
	// feeRate is the fee rate in sat/vB at the time first seen.
	feeRate float64
	// rbfCount is how many times this txid's fee has been bumped via RBF.
	rbfCount int
}

// spentOutpoint identifies a UTXO consumed by a mempool transaction.
// Used by RBF detection to find the original txid when a replacement arrives.
type spentOutpoint struct {
	txid string
	vout int
}

// addrCacheEntry is one entry in the per-userID watch-address cache inside
// MempoolTracker. Entries expire after EventsConfig.WatchAddrCacheTTL (default 5s).
type addrCacheEntry struct {
	addrs     []string
	fetchedAt time.Time
}

// MempoolTracker holds the in-process pending mempool map and wires the ZMQ
// display-tx and block handlers for the SSE events service.
//
// All exported Handle* methods satisfy the zmq.Subscriber handler signatures and
// are registered via RegisterRawTxHandler / RegisterBlockHandler in routes.go
// BEFORE btcSub.Run(ctx) is started.
//
// Thread-safety: all access to pendingMempool, spentOutpoints, and
// txidToOutpoints is guarded by mu (sync.Mutex, not RWMutex — every path is a
// write or a read-then-write; no read-only hot path exists). The watch-address
// cache is guarded by addrCacheMu (separate to avoid holding the main lock
// during Redis SScan calls).
type MempoolTracker struct {
	mu sync.Mutex

	// pendingMempool tracks mempool txids matched to at least one watch address.
	// key: txid — value: *pendingEntry.
	// Bounded to cfg.PendingMempoolMaxSize; new entries are dropped (not evicted)
	// when the cap is reached to avoid an expensive O(n) eviction scan on the hot path.
	pendingMempool map[string]*pendingEntry

	// spentOutpoints maps UTXOs consumed by pendingMempool txs → owning txid.
	// Used for RBF detection: when a new tx spends an input already in this map,
	// the original txid is retrieved and a "mempool_replaced" event is emitted
	// before the replacement is inserted.
	spentOutpoints map[spentOutpoint]string

	// txidToOutpoints is the reverse index of spentOutpoints: txid → []spentOutpoint.
	// Enables O(1) cleanup of spentOutpoints entries when a txid is confirmed or
	// pruned, avoiding the previous O(n) full-map scan.
	txidToOutpoints map[string][]spentOutpoint

	// store provides GetUserWatchAddresses lookups via Redis SScan.
	store Storer
	// broker receives EmitToUser calls for fan-out delivery.
	broker *Broker
	// rpc is used for GetMempoolEntry (fee rate) and GetBlockHeader +
	// GetBlock(verbosity=1) (block handler). GetRawTransaction is no longer
	// called on the display path — inputs and outputs arrive pre-decoded in
	// RawTxEvent from the rawtx ZMQ topic.
	rpc rpc.Client
	// cfg carries PendingMempoolMaxSize, MempoolPendingMaxAgeDays, and
	// BlockRPCTimeout used by both handlers and the pruning goroutine.
	cfg EventsConfig
	// rec is the narrow metrics recorder; nil-safe.
	rec mempoolRecorder

	// addrCacheMu guards addrCache. Separate from mu to avoid holding the
	// pendingMempool write lock during Redis SScan calls.
	addrCacheMu sync.Mutex
	// addrCache is a short-lived per-userID cache of watch addresses fetched from
	// Redis. Reduces SScan calls from O(tx/s × users) to O(1) cache hits under
	// steady state. Invalidated by TTL (EventsConfig.WatchAddrCacheTTL, default 5s).
	addrCache map[string]*addrCacheEntry

	// wg tracks the pruning goroutine so Shutdown can wait for it to exit.
	wg sync.WaitGroup
}

// NewMempoolTracker constructs a MempoolTracker and starts the hourly age-pruning
// goroutine. ctx must be the application root context; the goroutine exits when
// it is cancelled (i.e. when the server shuts down).
//
// rec may be nil — all metric calls are nil-guarded.
func NewMempoolTracker(ctx context.Context, store Storer, broker *Broker, rpcClient rpc.Client, rec mempoolRecorder, cfg EventsConfig) *MempoolTracker {
	t := &MempoolTracker{
		pendingMempool:  make(map[string]*pendingEntry),
		spentOutpoints:  make(map[spentOutpoint]string),
		txidToOutpoints: make(map[string][]spentOutpoint),
		addrCache:       make(map[string]*addrCacheEntry),
		store:           store,
		broker:          broker,
		rpc:             rpcClient,
		cfg:             cfg,
		rec:             rec,
	}
	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		t.runPruning(ctx)
	}()
	return t
}

// Shutdown waits for the pruning goroutine to exit.
// The goroutine exits when the ctx passed to NewMempoolTracker is cancelled.
// Call this after cancelling that context during graceful server shutdown.
func (t *MempoolTracker) Shutdown() {
	t.wg.Wait()
}

// ── ZMQ handlers ──────────────────────────────────────────────────────────────

// HandleRawTxEvent is registered via RegisterRawTxHandler and is called for
// every rawtx ZMQ message (a transaction entering the mempool).
//
// The ZMQ rawtx topic delivers the full serialized transaction bytes. ParseRawTx
// (in the zmq package) decodes them before this handler is called, so inputs and
// outputs are already available in e — no GetRawTransaction RPC call is needed.
// This eliminates the race condition where a transaction confirmed between the
// ZMQ event and the RPC call on a pruned node without txindex=1.
//
// Flow:
//  1. Build output address set from e.Outputs (already decoded, no RPC call).
//  2. Optionally fetch fee rate from GetMempoolEntry (non-fatal; display shows 0).
//  3. Phase 1 (no lock): for each connected user, fetch watch addresses and
//     compute intersection. Collect results into a userMatch slice.
//  4. Phase 2 (single lock per transaction): RBF detection, pendingMempool
//     insert/update for all matched users at once.
//  5. Phase 3 (no lock): fan-out emit to every matched user.
func (t *MempoolTracker) HandleRawTxEvent(ctx context.Context, e zmq.RawTxEvent) {
	txid := e.TxIDHex()

	// Build output address set directly from the pre-decoded RawTxEvent.
	// No RPC call needed — addresses were extracted from scriptPubKey bytes
	// during ParseRawTx in the zmq reader goroutine.
	txAddrs := make(map[string]struct{}, len(e.Outputs))
	for _, out := range e.Outputs {
		if out.Address != "" {
			txAddrs[out.Address] = struct{}{}
		}
	}
	if len(txAddrs) == 0 {
		return // OP_RETURN or unrecognised scripts only — nothing to match
	}

	// Compute fee rate (sat/vB) from the mempool entry.
	// Non-fatal: if unavailable (e.g. tx already confirmed), display shows 0.
	var feeRate float64
	rpcCtx, cancel := context.WithTimeout(ctx, t.cfg.BlockRPCTimeout)
	defer cancel()
	if me, merr := t.rpc.GetMempoolEntry(rpcCtx, txid); merr == nil && me.VSize > 0 {
		if satFee, ferr := rpc.BtcToSat(me.Fees.Base); ferr == nil {
			feeRate = float64(satFee) / float64(me.VSize)
		}
	}

	// Build spent-outpoint list from pre-decoded inputs (for RBF detection).
	spents := make([]spentOutpoint, 0, len(e.Inputs))
	for _, in := range e.Inputs {
		if !in.IsCoinbase() {
			spents = append(spents, spentOutpoint{txid: in.PrevTxIDHex, vout: int(in.PrevVout)})
		}
	}

	// ── Phase 1: collect per-user match info (no lock; Redis calls here) ──────

	type userMatch struct {
		userID  string
		matched []string
	}
	var matches []userMatch

	for _, userID := range t.broker.ConnectedUserIDs() {
		// Apply a per-call timeout so a Redis latency spike cannot stall this
		// handler indefinitely. Use WithoutCancel so the connection closing does
		// not abort address lookups for other users.
		// cachedWatchAddresses serves from an in-process TTL cache (default 5s)
		// to reduce SScan load from O(tx/s × users) to near-zero under steady state.
		addrCtx, addrCancel := context.WithTimeout(context.WithoutCancel(ctx), t.cfg.BlockRPCTimeout)
		addrs, aerr := t.cachedWatchAddresses(addrCtx, userID)
		addrCancel()
		if aerr != nil || len(addrs) == 0 {
			continue
		}

		var matched []string
		for _, a := range addrs {
			if _, ok := txAddrs[a]; ok {
				matched = append(matched, a)
			}
		}
		if len(matched) > 0 {
			matches = append(matches, userMatch{userID: userID, matched: matched})
		}
	}

	if len(matches) == 0 {
		return
	}

	// ── Phase 2: single lock for all mutations ────────────────────────────────
	//
	// RBF detection: check all spent outpoints once for the whole transaction.
	// Emit mempool_replaced to all matched users for each displaced txid.
	// Insert/update pendingMempool once for the whole transaction.
	//
	// Calling broker.EmitToUser inside the lock is safe: it takes its own b.mu
	// momentarily (to copy the channel slice) and the lock ordering t.mu → b.mu
	// has no inversion elsewhere.

	t.mu.Lock()

	// RBF detection: find replaced txids, deduplicate, emit to all matched users.
	replacedTxids := make(map[string]struct{})
	for _, sp := range spents {
		if origTxid, exists := t.spentOutpoints[sp]; exists && origTxid != txid {
			replacedTxids[origTxid] = struct{}{}
		}
	}
	for origTxid := range replacedTxids {
		if t.rec != nil {
			t.rec.OnRBFDetected()
		}
		for _, m := range matches {
			t.emitReplacedLocked(m.userID, origTxid)
		}
	}

	// Cap guard — silently drop and record metric when pendingMempool is full.
	if len(t.pendingMempool) >= t.cfg.PendingMempoolMaxSize {
		if t.rec != nil {
			t.rec.OnMempoolEntryDropped("cap_reached")
		}
		t.mu.Unlock()
		// Still emit pending_mempool events even when we didn't store the entry,
		// because users should see the event regardless of our internal cap.
		for _, m := range matches {
			t.emitPendingMempool(m.userID, txid, m.matched, feeRate)
		}
		return
	}

	entry := t.pendingMempool[txid]
	if entry == nil {
		entry = &pendingEntry{
			addrs:   make(map[string]struct{}),
			addedAt: time.Now(),
			feeRate: feeRate,
		}
		t.pendingMempool[txid] = entry
		// Register spent outpoints so future replacements are detectable.
		// Also populate the reverse index for O(1) cleanup.
		for _, sp := range spents {
			t.spentOutpoints[sp] = txid
			t.txidToOutpoints[txid] = append(t.txidToOutpoints[txid], sp)
		}
	} else {
		// Already tracked — this is an RBF bump (same inputs, higher fee).
		entry.rbfCount++
		entry.feeRate = feeRate
	}
	// Merge all matched addresses from all users into the entry.
	for _, m := range matches {
		for _, a := range m.matched {
			entry.addrs[a] = struct{}{}
		}
	}

	pendingSize := len(t.pendingMempool)
	t.mu.Unlock()

	if t.rec != nil {
		t.rec.SetPendingMempoolSize(pendingSize)
	}

	// ── Phase 3: emit events (no lock) ────────────────────────────────────────

	for _, m := range matches {
		t.emitPendingMempool(m.userID, txid, m.matched, feeRate)
	}
}

// HandleBlockEvent is registered via RegisterBlockHandler and is called for
// every hashblock ZMQ message.
//
// Flow:
//  1. GetBlockHeader → emit new_block to all connected users.
//  2. GetBlock(verbosity=1) → list of txids in the block.
//  3. For each txid present in pendingMempool:
//     a. Collect watched addresses from the entry (under write lock).
//     b. Remove from pendingMempool + spentOutpoints (via reverse index).
//     c. Emit confirmed_tx to each connected user whose watch set overlaps.
func (t *MempoolTracker) HandleBlockEvent(ctx context.Context, e zmq.BlockEvent) {
	hashHex := e.HashHex()

	rpcCtx, cancel := context.WithTimeout(ctx, t.cfg.BlockRPCTimeout)
	defer cancel()

	header, err := t.rpc.GetBlockHeader(rpcCtx, hashHex)
	if err != nil {
		return
	}

	// Emit new_block to all connected users.
	userIDs := t.broker.ConnectedUserIDs()
	newBlockPayload, _ := json.Marshal(map[string]any{
		"hash":   hashHex,
		"height": header.Height,
		"time":   header.Time,
	})
	newBlockEvent := Event{Type: "new_block", Payload: newBlockPayload}
	for _, uid := range userIDs {
		t.broker.EmitToUser(uid, newBlockEvent)
	}

	// GetBlock verbosity=1: block header + list of txids (no full tx data — efficient).
	rawBlock, berr := t.rpc.GetBlock(rpcCtx, hashHex, 1)
	if berr != nil {
		return
	}
	var blockV1 struct {
		Tx []string `json:"tx"`
	}
	if err := json.Unmarshal(rawBlock, &blockV1); err != nil {
		return
	}

	// Build user→watchAddresses map ONCE per block — O(users) SScan calls total
	// instead of O(confirmed_pending × users). The pre-built map is then used in
	// the txid loop without any further Redis calls, keeping the block handler latency
	// proportional to the number of connected users rather than to block density.
	userAddrs := make(map[string][]string, len(userIDs))
	for _, uid := range userIDs {
		addrCtx, addrCancel := context.WithTimeout(context.WithoutCancel(ctx), t.cfg.BlockRPCTimeout)
		addrs, aerr := t.cachedWatchAddresses(addrCtx, uid)
		addrCancel()
		if aerr == nil {
			userAddrs[uid] = addrs
		}
	}

	// For each confirmed tx, check if it was pending and deliver confirmed_tx.
	for _, txid := range blockV1.Tx {
		t.mu.Lock()
		entry, ok := t.pendingMempool[txid]
		if !ok {
			t.mu.Unlock()
			continue
		}

		// Collect tracked addresses and remove entry under write lock.
		watchedAddrs := make([]string, 0, len(entry.addrs))
		for a := range entry.addrs {
			watchedAddrs = append(watchedAddrs, a)
		}
		delete(t.pendingMempool, txid)
		// Use reverse index for O(1) spentOutpoints cleanup.
		for _, sp := range t.txidToOutpoints[txid] {
			delete(t.spentOutpoints, sp)
		}
		delete(t.txidToOutpoints, txid)

		pendingSize := len(t.pendingMempool)
		t.mu.Unlock()

		if t.rec != nil {
			t.rec.SetPendingMempoolSize(pendingSize)
		}

		// Build confirmed_tx payload once; fan-out per user using the pre-built map.
		cfPayload, _ := json.Marshal(map[string]any{
			"txid":      txid,
			"block":     hashHex,
			"height":    header.Height,
			"addresses": watchedAddrs,
		})
		cfEvent := Event{Type: "confirmed_tx", Payload: cfPayload}

		// Build a lookup set for O(1) address membership checks.
		watchedSet := make(map[string]struct{}, len(watchedAddrs))
		for _, a := range watchedAddrs {
			watchedSet[a] = struct{}{}
		}

		// Fan-out using pre-built map — no Redis calls inside this loop.
		for uid, addrs := range userAddrs {
			for _, a := range addrs {
				if _, hit := watchedSet[a]; hit {
					t.broker.EmitToUser(uid, cfEvent)
					break // only emit once per user per confirmed tx
				}
			}
		}
	}
}

// ── Emit helpers ──────────────────────────────────────────────────────────────

// emitPendingMempool sends a pending_mempool event to a single user.
func (t *MempoolTracker) emitPendingMempool(userID, txid string, addrs []string, feeRate float64) {
	payload, _ := json.Marshal(map[string]any{
		"txid":      txid,
		"addresses": addrs,
		"fee_rate":  feeRate,
	})
	t.broker.EmitToUser(userID, Event{Type: "pending_mempool", Payload: payload})
}

// emitReplacedLocked sends a mempool_replaced event to a single user for a
// displaced (RBF-evicted) transaction.
// Must be called with t.mu held (write lock).
func (t *MempoolTracker) emitReplacedLocked(userID, origTxid string) {
	payload, _ := json.Marshal(map[string]any{
		"replaced_txid": origTxid,
	})
	t.broker.EmitToUser(userID, Event{Type: "mempool_replaced", Payload: payload})
}

// ── Age-pruning goroutine ─────────────────────────────────────────────────────

// runPruning ticks hourly and removes entries older than MempoolPendingMaxAgeDays
// from pendingMempool and their corresponding spentOutpoints.
// Exits when ctx is cancelled (application shutdown).
func (t *MempoolTracker) runPruning(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			t.pruneOldEntries()
		case <-ctx.Done():
			return
		}
	}
}

// ── Watch-address cache ──────────────────────────────────────────────────────

// effectiveAddrCacheTTL returns the watch-address cache TTL in effect.
// Returns 0 when caching is disabled (negative config value).
// Falls back to 5s when the config value is zero (unset).
func effectiveAddrCacheTTL(cfg EventsConfig) time.Duration {
	if cfg.WatchAddrCacheTTL < 0 {
		return 0 // explicitly disabled
	}
	if cfg.WatchAddrCacheTTL == 0 {
		return 5 * time.Second // default
	}
	return cfg.WatchAddrCacheTTL
}

// cachedWatchAddresses returns watch addresses for userID from the in-process
// cache if the entry is fresh, otherwise fetches from Redis and caches the result.
//
// Cache TTL is controlled by EventsConfig.WatchAddrCacheTTL (default 5s).
// Set WatchAddrCacheTTL to a negative value to bypass the cache entirely
// (useful in integration tests where address set changes must be visible immediately).
func (t *MempoolTracker) cachedWatchAddresses(ctx context.Context, userID string) ([]string, error) {
	ttl := effectiveAddrCacheTTL(t.cfg)
	if ttl > 0 {
		t.addrCacheMu.Lock()
		entry, ok := t.addrCache[userID]
		if ok && time.Since(entry.fetchedAt) < ttl {
			addrs := entry.addrs
			t.addrCacheMu.Unlock()
			return addrs, nil
		}
		t.addrCacheMu.Unlock()
	}

	// Cache miss or caching disabled — fetch from Redis.
	addrs, err := t.store.GetUserWatchAddresses(ctx, userID)
	if err != nil {
		return nil, err
	}

	if ttl > 0 {
		t.addrCacheMu.Lock()
		t.addrCache[userID] = &addrCacheEntry{addrs: addrs, fetchedAt: time.Now()}
		t.addrCacheMu.Unlock()
	}
	return addrs, nil
}

// pruneOldEntries removes pendingMempool entries whose addedAt is older than
// cfg.MempoolPendingMaxAgeDays, along with their spentOutpoints (via reverse index).
func (t *MempoolTracker) pruneOldEntries() {
	cutoff := time.Now().AddDate(0, 0, -t.cfg.MempoolPendingMaxAgeDays)
	t.mu.Lock()
	defer t.mu.Unlock()

	pruned := 0
	for txid, entry := range t.pendingMempool {
		if entry.addedAt.Before(cutoff) {
			delete(t.pendingMempool, txid)
			// Use reverse index for O(1) spentOutpoints cleanup.
			for _, sp := range t.txidToOutpoints[txid] {
				delete(t.spentOutpoints, sp)
			}
			delete(t.txidToOutpoints, txid)
			pruned++
		}
	}

	if pruned > 0 && t.rec != nil {
		t.rec.OnMempoolPruned(pruned)
		t.rec.SetPendingMempoolSize(len(t.pendingMempool))
	}
}
