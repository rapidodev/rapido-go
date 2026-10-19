package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/legendary1205/rapido-go/internal/db/generated"
	"github.com/legendary1205/rapido-go/internal/sshexec"
	"github.com/legendary1205/rapido-go/internal/tunneldiscover"
	"github.com/legendary1205/rapido-go/internal/tunneldoctor"
)

// The Network Doctor endpoints: start a read-only diagnosis of a tunnel in the
// background (it takes about half a minute), poll its state, and fetch the
// report written for the datacenter's network team.

// diagnoseRunTimeout bounds a whole diagnosis, connection to report.
const diagnoseRunTimeout = 3 * time.Minute

func (h *Handler) doctorJobs() tunneldoctor.Jobs {
	return tunneldoctor.Jobs{KV: tunneldoctor.RedisKV{C: h.store.Cache}}
}

// controlPortOf is the relay port the node's frp client connects to: the one
// the panel allocated for a tunnel it built, else what detection found (the
// frp client config, then the HAProxy servers' port).
func controlPortOf(t generated.Tunnel, det tunneldiscover.Details) int {
	if t.Managed && t.FrpControlPort > 0 {
		return int(t.FrpControlPort)
	}
	if det.FRP != nil && det.FRP.ControlPort > 0 {
		return det.FRP.ControlPort
	}
	if det.HAProxy != nil {
		for _, s := range det.HAProxy.Servers {
			if _, port, err := net.SplitHostPort(s.Addr); err == nil {
				if n, err := strconv.Atoi(port); err == nil && n > 0 && n < 65536 {
					return n
				}
			}
		}
	}
	if !t.Managed {
		return 0
	}
	return int(t.FrpControlPort)
}

// doctorTarget gathers what the doctor needs from a tunnel row.
func doctorTarget(t generated.Tunnel, node generated.Node) tunneldoctor.Target {
	var det tunneldiscover.Details
	if len(t.Details) > 0 {
		_ = json.Unmarshal(t.Details, &det)
	}
	v6, v4 := tunneldiscover.PathAddrs(det, t.RelayTunnelAddr.String, t.RelayHost)
	target := tunneldoctor.Target{
		ID: t.ID, Name: t.Name, Method: t.Method, Protocol: det.Protocol,
		NodeSSH:  sshexec.Config{Host: node.Address, Port: t.NodeSshPort, User: t.NodeSshUser, Password: t.NodeSshPassword},
		RelaySSH: sshexec.Config{Host: t.RelayHost, Port: t.RelaySshPort, User: t.RelaySshUser, Password: t.RelaySshPassword},
		RelayV6:  v6, RelayV4: v4, ControlPort: controlPortOf(t, det),
		NodeNIC: det.NodeNIC, RelayNIC: det.RelayNIC,
	}
	if target.Protocol == "" {
		target.Protocol = methodLabel(t.Method)
	}
	if det.HAProxy != nil {
		target.HAProxySocket = det.HAProxy.Socket
	}
	return target
}

// historyLoader reads a tunnel's stored samples and events for the doctor.
func (h *Handler) historyLoader(tunnelID int32) tunneldoctor.HistoryFunc {
	return func(ctx context.Context, from, to time.Time) ([]tunneldoctor.HistorySample, []tunneldoctor.HistoryEvent, error) {
		rows, err := h.store.Queries.ListTunnelSamplesRaw(ctx, generated.ListTunnelSamplesRawParams{TunnelID: tunnelID, At: timestamptzFromTime(from)})
		if err != nil {
			return nil, nil, err
		}
		samples := make([]tunneldoctor.HistorySample, 0, len(rows))
		for _, r := range rows {
			samples = append(samples, historySampleFromRow(r))
		}
		evRows, err := h.store.Queries.ListTunnelEvents(ctx, generated.ListTunnelEventsParams{
			TunnelID: tunnelID, Since: timestamptzFromTime(from), RowLimit: 2000,
		})
		if err != nil {
			return nil, nil, err
		}
		events := make([]tunneldoctor.HistoryEvent, 0, len(evRows))
		for _, e := range evRows {
			events = append(events, tunneldoctor.HistoryEvent{
				At: e.At.Time, Kind: e.Kind, Severity: e.Severity, Detail: e.Detail, DurationSeconds: pgInt4ToPtr(e.DurationSeconds),
			})
		}
		return samples, events, nil
	}
}

func historySampleFromRow(r generated.TunnelSample) tunneldoctor.HistorySample {
	p8 := func(v float64, ok bool) *float64 {
		if !ok {
			return nil
		}
		return &v
	}
	var conn *float64
	if r.Connections.Valid {
		c := float64(r.Connections.Int32)
		conn = &c
	}
	return tunneldoctor.HistorySample{
		At:      r.At.Time,
		RelayRx: p8(r.RelayRxBps.Float64, r.RelayRxBps.Valid), RelayTx: p8(r.RelayTxBps.Float64, r.RelayTxBps.Valid),
		NodeRx: p8(r.NodeRxBps.Float64, r.NodeRxBps.Valid), NodeTx: p8(r.NodeTxBps.Float64, r.NodeTxBps.Valid),
		V6Loss: p8(float64(r.V6Loss.Float32), r.V6Loss.Valid), V4Loss: p8(float64(r.V4Loss.Float32), r.V4Loss.Valid),
		V6RTT: p8(float64(r.V6RttMs.Float32), r.V6RttMs.Valid), V4RTT: p8(float64(r.V4RttMs.Float32), r.V4RttMs.Valid),
		Connections: conn,
	}
}

// handleStartDiagnose implements POST /api/tunnels/:id/diagnose (sudo only):
// starts a diagnosis in the background and answers 202 with its state. When
// one is already running for the tunnel, answers 409 with that one's state.
func (h *Handler) handleStartDiagnose(c *gin.Context) {
	t, ok := h.tunnelFromParam(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	node, err := h.store.Queries.GetNodeByID(ctx, t.NodeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "could not look up this tunnel's node"})
		return
	}
	jobs := h.doctorJobs()
	started, job, err := jobs.Start(ctx, t.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "Could not start the diagnosis"})
		return
	}
	if !started {
		c.JSON(http.StatusConflict, gin.H{"detail": "A diagnosis is already running for this tunnel", "job": job})
		return
	}
	target := doctorTarget(t, node)
	go h.runDiagnosis(t.ID, job.StartedAt, target)
	c.JSON(http.StatusAccepted, job)
}

func (h *Handler) runDiagnosis(tunnelID int32, startedAt time.Time, target tunneldoctor.Target) {
	ctx, cancel := context.WithTimeout(context.Background(), diagnoseRunTimeout)
	defer cancel()
	res, err := tunneldoctor.Run(ctx, target, tunneldoctor.DialSSH, h.historyLoader(tunnelID), nil)
	if err != nil {
		h.logger.Warn("tunnel diagnosis failed", "tunnel_id", tunnelID, "error", err)
	}
	if res != nil {
		res.StartedAt = startedAt
	}
	if ferr := h.doctorJobs().Finish(ctx, tunnelID, startedAt, res, err); ferr != nil {
		h.logger.Warn("could not store the diagnosis", "tunnel_id", tunnelID, "error", ferr)
	}
}

// handleGetDiagnose implements GET /api/tunnels/:id/diagnose (sudo only): the
// state of the tunnel's latest diagnosis - running, done (with the structured
// result) or failed. 404 when none has been run in the last hour.
func (h *Handler) handleGetDiagnose(c *gin.Context) {
	t, ok := h.tunnelFromParam(c)
	if !ok {
		return
	}
	job, err := h.doctorJobs().Load(c.Request.Context(), t.ID)
	if errors.Is(err, tunneldoctor.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"detail": "No diagnosis has been run for this tunnel in the last hour"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "Could not read the diagnosis"})
		return
	}
	c.JSON(http.StatusOK, job)
}

var unsafeFilenameChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// reportFilename builds the download name of a report.
func reportFilename(tunnelName, lang string, at time.Time) string {
	name := unsafeFilenameChars.ReplaceAllString(tunnelName, "-")
	if name == "" || name == "-" {
		name = "tunnel"
	}
	return "network-report-" + name + "-" + at.UTC().Format("20060102-1504") + "." + lang + ".md"
}

// handleGetDiagnoseReport implements GET /api/tunnels/:id/diagnose/report
// (sudo only): the finished diagnosis as a Markdown report for the
// datacenter's network team. ?lang=en|fa (default en); ?download=1 sends it as
// a file. 404 when there is no diagnosis, 409 while it runs or if it failed.
func (h *Handler) handleGetDiagnoseReport(c *gin.Context) {
	t, ok := h.tunnelFromParam(c)
	if !ok {
		return
	}
	job, err := h.doctorJobs().Load(c.Request.Context(), t.ID)
	if errors.Is(err, tunneldoctor.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"detail": "No diagnosis has been run for this tunnel in the last hour"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "Could not read the diagnosis"})
		return
	}
	switch job.Status {
	case tunneldoctor.StatusRunning:
		c.JSON(http.StatusConflict, gin.H{"detail": "The diagnosis is still running"})
		return
	case tunneldoctor.StatusFailed:
		c.JSON(http.StatusConflict, gin.H{"detail": "The diagnosis failed: " + job.Error})
		return
	}
	lang := tunneldoctor.NormalizeLang(c.Query("lang"))
	md := tunneldoctor.BuildReport(job.Result, lang)
	if c.Query("download") == "1" {
		c.Header("Content-Disposition", `attachment; filename="`+reportFilename(t.Name, lang, job.Result.FinishedAt)+`"`)
	}
	c.Data(http.StatusOK, "text/markdown; charset=utf-8", []byte(md))
}
