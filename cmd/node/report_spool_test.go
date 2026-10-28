package main

import (
	"context"
	"encoding/json"
	"github.com/legendary1205/rapido-go/internal/nodecore/traffic"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRejectedUsageSurvivesRestartAndRetainsReportID(t *testing.T) {
	accepted := false
	var ids []string
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body reportRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		ids = append(ids, body.ReportID)
		if !accepted {
			w.WriteHeader(503)
		}
	}))
	defer panel.Close()
	cfg := config{PanelURL: panel.URL, ReportSecret: "test", StateDir: t.TempDir()}
	manager := traffic.NewManager()
	manager.Add("metered", 10, 20)
	node := &server{traffic: manager, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := node.pushReport(context.Background(), panel.Client(), cfg); err == nil {
		t.Fatal("503 was acknowledged")
	}
	replacement := &server{traffic: traffic.NewManager(), logger: node.logger}
	accepted = true
	if err := replacement.pushReport(context.Background(), panel.Client(), cfg); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] == "" || ids[0] != ids[1] {
		t.Fatalf("report identity changed across retry: %v", ids)
	}
}
