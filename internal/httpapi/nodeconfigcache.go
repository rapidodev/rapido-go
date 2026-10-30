package httpapi

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"
)

const (
	dataVersionNodeConfig = "node_config"

	// nodeConfigSafetyTTL bounds how long a cached payload can outlive a
	// change the triggers somehow did not report (see migration 00015). The
	// data version is what normally decides freshness; this only limits the
	// damage of a bug in that.
	nodeConfigSafetyTTL = 60 * time.Second

	// nodeConfigHistory is how many earlier payloads a profile keeps to send deltas
	// from. A node polls every ten seconds, and while users change every second it
	// comes back that many versions behind: with too few kept it gets the whole
	// list again on every poll, 2.7 MB per node at 50,000 users.
	nodeConfigHistory = 12

	// nodeConfigBuildTimeout is the budget of a rebuild that several polls
	// are waiting on. It is deliberately not the first caller's request
	// context: that caller giving up must not fail everyone sharing its build.
	nodeConfigBuildTimeout = 2 * time.Minute
)

// nodeConfigEntry is one profile's finished response, immutable once stored.
type nodeConfigEntry struct {
	version int64
	builtAt time.Time
	etag    string // quoted, ready for the ETag header
	body    []byte
}

// nodeConfigCache holds the current node-config payloads in memory, keyed by
// profile. In memory rather than Redis: a hit is then a map lookup instead of
// pulling ~1 MB across the network, and each panel process validating its
// copy against the shared data version needs no invalidation message.
type nodeConfigCache struct {
	mu       sync.Mutex
	snap     *nodeConfigSnapshot
	entries  map[string]*nodeConfigEntry
	previous map[string][]*nodeConfigEntry

	snapFlight  singleflight.Group
	entryFlight singleflight.Group

	// Counted so a test can prove a poll did not rebuild.
	snapshotLoads atomic.Int64
	renders       atomic.Int64

	// unservable is the last list noteUnservable logged.
	unservable string
}

// noteUnservable logs the rows a snapshot left out, once per distinct list: a
// snapshot is rebuilt on every user change, and the same few names each time
// would bury everything else in the log.
func (c *nodeConfigCache) noteUnservable(logger *slog.Logger, rows []string) {
	summary := strings.Join(rows, "; ")
	c.mu.Lock()
	changed := summary != c.unservable
	c.unservable = summary
	c.mu.Unlock()
	if !changed || len(rows) == 0 {
		return
	}
	logger.Warn("node config: users left out of every node's list", "count", len(rows), "users", rows[:min(len(rows), 20)])
}

func (c *nodeConfigCache) lookup(key string, version int64) *nodeConfigEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entries[key]
	if e == nil || e.version != version || time.Since(e.builtAt) >= nodeConfigSafetyTTL {
		return nil
	}
	return e
}

// store keeps e and drops everything that can no longer be served: entries of
// an older data version, and expired ones (a profile nobody polls any more
// would otherwise stay resident forever).
func (c *nodeConfigCache) store(key string, e *nodeConfigEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]*nodeConfigEntry{}
	}
	if c.previous == nil {
		c.previous = map[string][]*nodeConfigEntry{}
	}
	now := time.Now()
	for k, old := range c.entries {
		if old.version < e.version || (k == key && old.etag != e.etag) {
			if now.Sub(old.builtAt) < nodeConfigSafetyTTL {
				history := append([]*nodeConfigEntry{old}, c.previous[k]...)
				if len(history) > nodeConfigHistory {
					history = history[:nodeConfigHistory]
				}
				c.previous[k] = history
			}
			delete(c.entries, k)
		} else if now.Sub(old.builtAt) >= nodeConfigSafetyTTL {
			delete(c.entries, k)
		}
	}
	for k, history := range c.previous {
		valid := history[:0]
		for _, old := range history {
			if now.Sub(old.builtAt) < nodeConfigSafetyTTL {
				valid = append(valid, old)
			}
		}
		if len(valid) == 0 {
			delete(c.previous, k)
		} else {
			c.previous[k] = valid
		}
	}
	c.entries[key] = e
}

func (c *nodeConfigCache) lookupSnapshot(version int64) *nodeConfigSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s := c.snap; s != nil && s.version == version && time.Since(s.builtAt) < nodeConfigSafetyTTL {
		return s
	}
	return nil
}

func (c *nodeConfigCache) storeSnapshot(s *nodeConfigSnapshot) {
	c.mu.Lock()
	c.snap = s
	c.mu.Unlock()
}

// snapshotFor returns the fleet snapshot for a data version, loading it once
// however many profiles are asking.
func (h *Handler) snapshotFor(ctx context.Context, version int64) (*nodeConfigSnapshot, error) {
	c := &h.nodeConfig
	if s := c.lookupSnapshot(version); s != nil {
		return s, nil
	}
	v, err, _ := c.snapFlight.Do(strconv.FormatInt(version, 10), func() (any, error) {
		if s := c.lookupSnapshot(version); s != nil {
			return s, nil
		}
		s, err := h.loadNodeConfigSnapshot(ctx, version)
		if err != nil {
			return nil, err
		}
		c.storeSnapshot(s)
		return s, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(*nodeConfigSnapshot), nil
}

func (h *Handler) renderNodeConfigEntry(snap *nodeConfigSnapshot, p nodeProfile) (*nodeConfigEntry, error) {
	return h.renderNodeConfigEntrySchema(snap, p, nodeConfigSchema{})
}

func (h *Handler) renderNodeConfigEntrySchema(snap *nodeConfigSnapshot, p nodeProfile, schema nodeConfigSchema) (*nodeConfigEntry, error) {
	h.nodeConfig.renders.Add(1)
	payload, canonical, err := snap.renderSchema(p, schema)
	if err != nil {
		return nil, err
	}
	body, err := nodeConfigBodyBytes(payload, canonical)
	if err != nil {
		return nil, err
	}
	return &nodeConfigEntry{
		version: snap.version, builtAt: time.Now(),
		etag: `"` + payload.Version + `"`, body: body,
	}, nil
}

// cachedNodeConfig is the whole change-detection scheme: one tiny query for
// the current data version, and only when it differs from the version the
// stored payload was built at is anything rebuilt (once, however many nodes
// poll at that moment).
//
// The version is read BEFORE the data is, so a payload is always at least as
// new as the version it is filed under: a change landing mid-build makes the
// next poll see a higher version and rebuild, never serve something older
// than the version claims.
func (h *Handler) cachedNodeConfig(ctx context.Context, p nodeProfile) (*nodeConfigEntry, error) {
	return h.cachedNodeConfigSchema(ctx, p, nodeConfigSchema{})
}

func (h *Handler) cachedNodeConfigSchema(ctx context.Context, p nodeProfile, schema nodeConfigSchema) (*nodeConfigEntry, error) {
	c := &h.nodeConfig
	version, err := h.store.Queries.GetDataVersion(ctx, dataVersionNodeConfig)
	if err != nil {
		// Without a version there is no telling whether anything changed, so
		// build fresh and keep nothing.
		h.logger.Warn("node config: data version unreadable, serving an uncached build", "error", err)
		snap, err := h.loadNodeConfigSnapshot(ctx, 0)
		if err != nil {
			return nil, err
		}
		return h.renderNodeConfigEntrySchema(snap, p, schema)
	}

	key := schema.cacheKey(p)
	if e := c.lookup(key, version); e != nil {
		return e, nil
	}
	v, err, _ := c.entryFlight.Do(key+"@"+strconv.FormatInt(version, 10), func() (any, error) {
		if e := c.lookup(key, version); e != nil {
			return e, nil
		}
		bctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), nodeConfigBuildTimeout)
		defer cancel()
		snap, err := h.snapshotFor(bctx, version)
		if err != nil {
			return nil, err
		}
		e, err := h.renderNodeConfigEntrySchema(snap, p, schema)
		if err != nil {
			return nil, err
		}
		c.store(key, e)
		return e, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(*nodeConfigEntry), nil
}
