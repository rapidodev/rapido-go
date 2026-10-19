package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/legendary1205/rapido-go/internal/db/generated"
)

// History endpoints of the Tunnels page: the events internal/tunnelwatch
// derived (failover, packet loss, ceiling, ...) and the per-round samples,
// down-sampled so a chart never receives more than a few hundred points.

type tunnelEventDTO struct {
	ID         int64   `json:"id"`
	TunnelID   int32   `json:"tunnel_id"`
	TunnelName *string `json:"tunnel_name,omitempty"`
	// At is when the event happened (RFC 3339, UTC).
	At time.Time `json:"at"`
	// Kind is one of tunnel_down, tunnel_up, failover, failback, loss_high,
	// ceiling, nic_saturated; Severity is info, warning or critical.
	Kind     string `json:"kind"`
	Severity string `json:"severity"`
	// Detail is a sentence built only from the measurements behind the event.
	Detail string `json:"detail"`
	// DurationSeconds is set on tunnel_up: how long the tunnel had been down.
	DurationSeconds *int32 `json:"duration_seconds"`
}

func toTunnelEventDTO(e generated.TunnelEvent) tunnelEventDTO {
	return tunnelEventDTO{
		ID: e.ID, TunnelID: e.TunnelID, At: e.At.Time.UTC(), Kind: e.Kind, Severity: e.Severity, Detail: e.Detail,
		DurationSeconds: pgInt4ToPtr(e.DurationSeconds),
	}
}

// parseSince reads a ?since= value: RFC 3339 or unix seconds.
func parseSince(raw string) (time.Time, bool) {
	if raw == "" {
		return time.Time{}, false
	}
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n > 0 {
		return time.Unix(n, 0).UTC(), true
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC(), true
	}
	return time.Time{}, false
}

// clampInt reads an integer query value, falling back to def and keeping the
// result inside [lo, hi].
func clampInt(raw string, def, lo, hi int) int {
	n := def
	if raw != "" {
		if v, err := strconv.Atoi(raw); err == nil {
			n = v
		}
	}
	if n < lo {
		n = lo
	}
	if n > hi {
		n = hi
	}
	return n
}

func (h *Handler) tunnelFromParam(c *gin.Context) (generated.Tunnel, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": "Invalid id"})
		return generated.Tunnel{}, false
	}
	t, err := h.store.Queries.GetTunnel(c.Request.Context(), int32(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"detail": "Tunnel not found"})
		return generated.Tunnel{}, false
	}
	return t, true
}

// handleListTunnelEvents implements GET /api/tunnels/:id/events (sudo only):
// the tunnel's events, newest first. ?limit= (default 100, max 1000), ?since=
// (RFC 3339 or unix seconds, default 30 days back), ?kind= to filter.
func (h *Handler) handleListTunnelEvents(c *gin.Context) {
	t, ok := h.tunnelFromParam(c)
	if !ok {
		return
	}
	since := time.Now().Add(-30 * 24 * time.Hour)
	if raw := c.Query("since"); raw != "" {
		s, ok := parseSince(raw)
		if !ok {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": "since must be an RFC 3339 time or unix seconds"})
			return
		}
		since = s
	}
	var kind *string
	if k := c.Query("kind"); k != "" {
		kind = &k
	}
	rows, err := h.store.Queries.ListTunnelEvents(c.Request.Context(), generated.ListTunnelEventsParams{
		TunnelID: t.ID, Since: timestamptzFromTime(since), Kind: textFromPtr(kind),
		RowLimit: int32(clampInt(c.Query("limit"), 100, 1, 1000)),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "Could not read events"})
		return
	}
	out := make([]tunnelEventDTO, 0, len(rows))
	for _, r := range rows {
		out = append(out, toTunnelEventDTO(r))
	}
	c.JSON(http.StatusOK, gin.H{"tunnel_id": t.ID, "events": out})
}

// handleTunnelsTimeline implements GET /api/tunnels/timeline (sudo only):
// events of every tunnel inside the last ?hours= (default 24, max 720),
// newest first, capped at ?limit= (default 500, max 2000).
func (h *Handler) handleTunnelsTimeline(c *gin.Context) {
	hours := clampInt(c.Query("hours"), 24, 1, 720)
	rows, err := h.store.Queries.ListTunnelEventsTimeline(c.Request.Context(), generated.ListTunnelEventsTimelineParams{
		Since:    timestamptzFromTime(time.Now().Add(-time.Duration(hours) * time.Hour)),
		RowLimit: int32(clampInt(c.Query("limit"), 500, 1, 2000)),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "Could not read the timeline"})
		return
	}
	out := make([]tunnelEventDTO, 0, len(rows))
	for _, r := range rows {
		name := r.TunnelName
		out = append(out, tunnelEventDTO{
			ID: r.ID, TunnelID: r.TunnelID, TunnelName: &name, At: r.At.Time.UTC(), Kind: r.Kind, Severity: r.Severity,
			Detail: r.Detail, DurationSeconds: pgInt4ToPtr(r.DurationSeconds),
		})
	}
	c.JSON(http.StatusOK, gin.H{"hours": hours, "events": out})
}

// samplePointDTO is one chart point: the mean of the samples in a bucket, with
// the peak next to the throughput and loss means (an average hides exactly the
// flat top and the short loss burst the charts are for). Every measurement is
// null when nothing was measured in that bucket.
type samplePointDTO struct {
	T time.Time `json:"t"`
	// Bits per second on the relay's and the node's main network card.
	RelayRxBps    *float64 `json:"relay_rx_bps"`
	RelayTxBps    *float64 `json:"relay_tx_bps"`
	NodeRxBps     *float64 `json:"node_rx_bps"`
	NodeTxBps     *float64 `json:"node_tx_bps"`
	RelayRxBpsMax *float64 `json:"relay_rx_bps_max"`
	RelayTxBpsMax *float64 `json:"relay_tx_bps_max"`
	NodeRxBpsMax  *float64 `json:"node_rx_bps_max"`
	NodeTxBpsMax  *float64 `json:"node_tx_bps_max"`
	Connections   *float64 `json:"connections"`
	CPU           *float64 `json:"cpu"`
	// Path quality from the node to the relay: round trip and jitter in
	// milliseconds, loss in percent.
	V6RttMs    *float64 `json:"v6_rtt_ms"`
	V6Loss     *float64 `json:"v6_loss"`
	V6LossMax  *float64 `json:"v6_loss_max"`
	V6JitterMs *float64 `json:"v6_jitter_ms"`
	V4RttMs    *float64 `json:"v4_rtt_ms"`
	V4Loss     *float64 `json:"v4_loss"`
	V4LossMax  *float64 `json:"v4_loss_max"`
	V4JitterMs *float64 `json:"v4_jitter_ms"`
	// HAProxy: the server carrying the sessions at the end of the bucket, and
	// whether the primary / backup server was up in every sample of it. null
	// when the node has no HAProxy.
	HAActive    *string `json:"ha_active"`
	HAPrimaryUp *bool   `json:"ha_primary_up"`
	HABackupUp  *bool   `json:"ha_backup_up"`
	Samples     int32   `json:"samples"`
}

// maxChartPoints is the most points a samples response carries.
const maxChartPoints = 300

// bucketSeconds picks the bin width so a window of the given hours has at
// most maxChartPoints bins, never finer than the one-minute collection round.
func bucketSeconds(hours int) int {
	secs := (hours*3600 + maxChartPoints - 1) / maxChartPoints
	if secs < 60 {
		secs = 60
	}
	return secs
}

// noData maps the -1 the binned query uses for "nothing measured" to nil.
func noData(v float64) *float64 {
	if v < 0 {
		return nil
	}
	return &v
}

// triState maps the query's 1 / 0 / -1 to true / false / nil.
func triState(v int32) *bool {
	if v < 0 {
		return nil
	}
	b := v == 1
	return &b
}

func toSamplePoint(r generated.ListTunnelSamplesBinnedRow) samplePointDTO {
	p := samplePointDTO{
		T:             r.Bucket.Time.UTC(),
		RelayRxBps:    noData(r.RelayRxBps),
		RelayTxBps:    noData(r.RelayTxBps),
		NodeRxBps:     noData(r.NodeRxBps),
		NodeTxBps:     noData(r.NodeTxBps),
		RelayRxBpsMax: noData(r.RelayRxBpsMax),
		RelayTxBpsMax: noData(r.RelayTxBpsMax),
		NodeRxBpsMax:  noData(r.NodeRxBpsMax),
		NodeTxBpsMax:  noData(r.NodeTxBpsMax),
		Connections:   noData(r.Connections),
		CPU:           noData(r.Cpu),
		V6RttMs:       noData(r.V6RttMs), V6Loss: noData(r.V6Loss), V6LossMax: noData(r.V6LossMax), V6JitterMs: noData(r.V6JitterMs),
		V4RttMs: noData(r.V4RttMs), V4Loss: noData(r.V4Loss), V4LossMax: noData(r.V4LossMax), V4JitterMs: noData(r.V4JitterMs),
		HAPrimaryUp: triState(r.HaPrimaryUp), HABackupUp: triState(r.HaBackupUp),
		Samples: r.Samples,
	}
	if r.HaActive != "" {
		active := r.HaActive
		p.HAActive = &active
	}
	return p
}

// handleTunnelSamples implements GET /api/tunnels/:id/samples (sudo only):
// the tunnel's stored rounds of the last ?hours= (default 24, max 336 - the
// retention is 14 days), averaged into at most 300 buckets, plus the events of
// the same window so a chart can mark them.
func (h *Handler) handleTunnelSamples(c *gin.Context) {
	t, ok := h.tunnelFromParam(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	hours := clampInt(c.Query("hours"), 24, 1, 336)
	since := time.Now().Add(-time.Duration(hours) * time.Hour)
	bucket := bucketSeconds(hours)

	rows, err := h.store.Queries.ListTunnelSamplesBinned(ctx, generated.ListTunnelSamplesBinnedParams{
		BucketSecs: float64(bucket), TunnelID: t.ID, Since: timestamptzFromTime(since),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "Could not read samples"})
		return
	}
	points := make([]samplePointDTO, 0, len(rows))
	for _, r := range rows {
		points = append(points, toSamplePoint(r))
	}

	events := []tunnelEventDTO{}
	if evRows, err := h.store.Queries.ListTunnelEvents(ctx, generated.ListTunnelEventsParams{
		TunnelID: t.ID, Since: timestamptzFromTime(since), RowLimit: 200,
	}); err == nil {
		for _, r := range evRows {
			events = append(events, toTunnelEventDTO(r))
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"tunnel_id": t.ID, "hours": hours, "bucket_seconds": bucket, "since": since.UTC(),
		"points": points, "events": events,
	})
}
