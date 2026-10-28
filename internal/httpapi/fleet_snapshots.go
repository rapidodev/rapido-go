package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/legendary1205/rapido-go/internal/db/generated"
	"github.com/legendary1205/rapido-go/internal/txscope"
)

// Account mutations use their own transactional policy checks. Structural fleet
// edits additionally share one lock so a snapshot is a coherent configuration.
func structuralConfigMutation(method, path string) bool {
	if method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions {
		return false
	}
	if strings.HasSuffix(path, "/validate") || strings.HasSuffix(path, "/restart") || strings.HasSuffix(path, "/reconnect") || strings.HasSuffix(path, "/capacity") {
		return false
	}
	return path == "/api/hosts" || strings.HasPrefix(path, "/api/inbounds") ||
		path == "/api/settings/core-config" || path == "/api/settings/xray-config" ||
		path == "/api/core/config" || strings.HasSuffix(path, "/restore") && strings.HasPrefix(path, "/api/core/config/backups/") ||
		path == "/api/node" || strings.HasPrefix(path, "/api/node/") && (method == http.MethodPut || method == http.MethodDelete)
}

func (h *Handler) saveFleetSnapshot(ctx context.Context, reason string) error {
	if txscope.Tx(ctx) == nil {
		return fmt.Errorf("fleet snapshot requires a transaction")
	}
	raw, err := h.store.Queries.CaptureFleetConfig(ctx)
	if err != nil {
		return err
	}
	return h.store.Queries.SaveCoreSnapshot(ctx, generated.SaveCoreSnapshotParams{Config: raw, Reason: reason})
}

func (h *Handler) restoreFleetSnapshot(ctx context.Context, raw []byte) (coreConfigDTO, error) {
	var snapshot struct {
		Version int           `json:"snapshot_version"`
		Core    coreConfigDTO `json:"core"`
	}
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return coreConfigDTO{}, fmt.Errorf("invalid snapshot: %w", err)
	}
	if snapshot.Version == 0 {
		// Snapshots made by the initial routing-only implementation.
		if err := json.Unmarshal(raw, &snapshot.Core); err != nil {
			return coreConfigDTO{}, err
		}
		return h.applyCoreConfig(ctx, snapshot.Core)
	}
	if snapshot.Version != 1 || txscope.Tx(ctx) == nil {
		return coreConfigDTO{}, fmt.Errorf("unsupported snapshot or missing transaction")
	}
	q := h.store.Queries
	for _, restore := range []func(context.Context, []byte) error{
		q.RestoreFleetInbounds, q.DeleteNonSnapshotInbounds,
	} {
		if err := restore(ctx, raw); err != nil {
			return coreConfigDTO{}, err
		}
	}
	if err := q.DeleteFleetHosts(ctx); err != nil {
		return coreConfigDTO{}, err
	}
	if err := q.RestoreFleetHosts(ctx, raw); err != nil {
		return coreConfigDTO{}, err
	}
	if err := q.ClearFleetExclusions(ctx); err != nil {
		return coreConfigDTO{}, err
	}
	if err := q.RestoreFleetExclusions(ctx, raw); err != nil {
		return coreConfigDTO{}, err
	}
	if err := q.ClearFleetTemplateInbounds(ctx); err != nil {
		return coreConfigDTO{}, err
	}
	if err := q.RestoreFleetTemplateInbounds(ctx, raw); err != nil {
		return coreConfigDTO{}, err
	}
	if err := q.RestoreFleetNodeProfiles(ctx, raw); err != nil {
		return coreConfigDTO{}, err
	}
	// Validation sees the restored inbound tags and node overrides. Any error
	// rolls back the whole restore, including all associations and hosts.
	saved, err := h.applyCoreConfig(ctx, snapshot.Core)
	if err != nil {
		return coreConfigDTO{}, err
	}
	if err := h.store.Cache.InvalidateFleet(ctx); err != nil {
		return coreConfigDTO{}, err
	}
	return saved, nil
}
