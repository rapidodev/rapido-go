package httpapi

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/legendary1205/rapido-go/internal/db/generated"
	"github.com/legendary1205/rapido-go/internal/nodecontrol"
)

// Outbound tests run on the nodes, because outbounds only exist there: the
// panel asks each node over its mTLS control plane to dial through the
// outbound and time one request (see cmd/node's handleTestOutbounds). The same
// outbound can behave differently on every node - a WireGuard exit is a tunnel
// on one server and a plain direct fallback everywhere else - so the answer is
// per node.
const (
	// outboundProbeMaxTags caps one request; the dashboard tests a long list in
	// batches of fewer.
	outboundProbeMaxTags = 16
	// outboundProbeNodeParallel is how many nodes are asked at once.
	outboundProbeNodeParallel = 8
	// outboundProbeNodeTimeout bounds the call to one node. The node keeps its
	// own work inside 18 s, so a slow node answers rather than times out.
	outboundProbeNodeTimeout = 22 * time.Second
	// outboundProbeWait is how long a request waits for one of
	// outboundProbeSlots before it is turned away.
	outboundProbeWait = 5 * time.Second
)

// outboundProbeSlots bounds the tests this panel process runs at once, so a
// burst of clicks cannot fan out into hundreds of node calls. The nodes bound
// the tests they run themselves as well.
var outboundProbeSlots = make(chan struct{}, 4)

// outboundTester runs outbound tests on one node.
type outboundTester interface {
	TestOutbounds(ctx context.Context, node generated.Node, tags []string) (nodecontrol.OutboundTestResponse, error)
}

type controlPlaneTester struct{ client *nodecontrol.Client }

func (t controlPlaneTester) TestOutbounds(ctx context.Context, node generated.Node, tags []string) (nodecontrol.OutboundTestResponse, error) {
	return t.client.TestOutbounds(ctx, node.Address, int(node.ApiPort), tags)
}

// nodeControlCache holds the control-plane client, built from the CA in the
// `tls` table and rebuilt if that ever changes (a restored backup).
type nodeControlCache struct {
	mu      sync.Mutex
	certPEM string
	client  *nodecontrol.Client
}

func (h *Handler) nodeControl(ctx context.Context) (outboundTester, error) {
	if h.outboundTester != nil {
		return h.outboundTester, nil
	}
	ca, err := h.store.Queries.GetTLS(ctx)
	if err != nil {
		return nil, err
	}
	h.nodeCtl.mu.Lock()
	defer h.nodeCtl.mu.Unlock()
	if h.nodeCtl.client == nil || h.nodeCtl.certPEM != ca.Certificate {
		client, err := nodecontrol.New(ca.Certificate, ca.Key)
		if err != nil {
			return nil, err
		}
		if h.nodeCtl.client != nil {
			h.nodeCtl.client.CloseIdleConnections()
		}
		h.nodeCtl.certPEM, h.nodeCtl.client = ca.Certificate, client
	}
	return controlPlaneTester{h.nodeCtl.client}, nil
}

type outboundProbeRequest struct {
	Tags []string `json:"tags"`
	// NodeID picks one node; 0 means every connected node.
	NodeID int32 `json:"node_id"`
}

type outboundProbeNode struct {
	ID   int32  `json:"id"`
	Name string `json:"name"`
	// Error is why this node gave no results at all (unreachable, no core).
	Error string `json:"error,omitempty"`
}

// outboundProbeResult is one outbound on one node. A node that could not be
// asked has a result for every tag, with its Error.
type outboundProbeResult struct {
	NodeID int32  `json:"node_id"`
	Node   string `json:"node"`
	nodecontrol.OutboundTestResult
}

type outboundProbeResponse struct {
	URL     string                `json:"url"`
	Nodes   []outboundProbeNode   `json:"nodes"`
	Results []outboundProbeResult `json:"results"`
}

// handleTestOutbounds implements POST /api/core/outbounds/test (sudo only):
// test the outbounds named by tags on one node (node_id) or on every connected
// node. It changes nothing, which is why it lives outside
// /api/settings/core-config: that prefix runs in a locked transaction, and a
// test holds its request for seconds.
func (h *Handler) handleTestOutbounds(c *gin.Context) {
	var req outboundProbeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": err.Error()})
		return
	}
	var tags []string
	for _, tag := range req.Tags {
		tag = strings.TrimSpace(tag)
		if tag != "" && !slices.Contains(tags, tag) {
			tags = append(tags, tag)
		}
	}
	if len(tags) == 0 || len(tags) > outboundProbeMaxTags {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": "tags: give 1 to " + strconv.Itoa(outboundProbeMaxTags) + " outbound tags"})
		return
	}

	ctx := c.Request.Context()
	nodes, status, msg := h.outboundProbeNodes(ctx, req.NodeID)
	if msg != "" {
		c.JSON(status, gin.H{"detail": msg})
		return
	}
	tester, err := h.nodeControl(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "Could not load the panel's node identity"})
		return
	}

	wait, cancel := context.WithTimeout(ctx, outboundProbeWait)
	defer cancel()
	select {
	case outboundProbeSlots <- struct{}{}:
		defer func() { <-outboundProbeSlots }()
	case <-wait.Done():
		c.JSON(http.StatusTooManyRequests, gin.H{"detail": "Other outbound tests are still running; try again in a moment"})
		return
	}
	c.JSON(http.StatusOK, probeOutbounds(ctx, tester, nodes, tags))
}

// outboundProbeNodes is the node asked for, or every connected node.
func (h *Handler) outboundProbeNodes(ctx context.Context, id int32) ([]generated.Node, int, string) {
	if id != 0 {
		node, err := h.store.Queries.GetNodeByID(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, http.StatusNotFound, "Node not found"
		}
		if err != nil {
			return nil, http.StatusInternalServerError, "Could not load the node"
		}
		if node.Status == "disabled" {
			return nil, http.StatusConflict, "This node is disabled"
		}
		return []generated.Node{node}, 0, ""
	}
	all, err := h.store.Queries.ListNodes(ctx)
	if err != nil {
		return nil, http.StatusInternalServerError, "Could not list the nodes"
	}
	var nodes []generated.Node
	for _, n := range all {
		if n.Status == "connected" {
			nodes = append(nodes, n)
		}
	}
	if len(nodes) == 0 {
		return nil, http.StatusConflict, "No node is connected to test from"
	}
	return nodes, 0, ""
}

// probeOutbounds asks every node, a few at a time, and lays the answers out by
// tag, then node, in the order given.
func probeOutbounds(ctx context.Context, tester outboundTester, nodes []generated.Node, tags []string) outboundProbeResponse {
	answers := make([]nodecontrol.OutboundTestResponse, len(nodes))
	failures := make([]error, len(nodes))
	slots := make(chan struct{}, outboundProbeNodeParallel)
	var wg sync.WaitGroup
	for i, node := range nodes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-ctx.Done():
				failures[i] = ctx.Err()
				return
			}
			call, cancel := context.WithTimeout(ctx, outboundProbeNodeTimeout)
			defer cancel()
			answers[i], failures[i] = tester.TestOutbounds(call, node, tags)
		}()
	}
	wg.Wait()

	resp := outboundProbeResponse{Nodes: make([]outboundProbeNode, len(nodes))}
	byNode := make([]map[string]nodecontrol.OutboundTestResult, len(nodes))
	for i, node := range nodes {
		resp.Nodes[i] = outboundProbeNode{ID: node.ID, Name: node.Name}
		if failures[i] != nil {
			resp.Nodes[i].Error = failures[i].Error()
			continue
		}
		if resp.URL == "" {
			resp.URL = answers[i].URL
		}
		byNode[i] = make(map[string]nodecontrol.OutboundTestResult, len(answers[i].Results))
		for _, r := range answers[i].Results {
			byNode[i][r.Tag] = r
		}
	}
	resp.Results = make([]outboundProbeResult, 0, len(tags)*len(nodes))
	for _, tag := range tags {
		for i, node := range nodes {
			r, ok := byNode[i][tag]
			switch {
			case failures[i] != nil:
				r = nodecontrol.OutboundTestResult{Tag: tag, Error: resp.Nodes[i].Error}
			case !ok:
				r = nodecontrol.OutboundTestResult{Tag: tag, Error: "the node did not answer for this outbound"}
			}
			resp.Results = append(resp.Results, outboundProbeResult{NodeID: node.ID, Node: node.Name, OutboundTestResult: r})
		}
	}
	return resp
}
