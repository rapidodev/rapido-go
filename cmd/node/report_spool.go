package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/legendary1205/rapido-go/internal/hostmetrics"
	"github.com/legendary1205/rapido-go/internal/nodecore/traffic"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

type reportSpool struct {
	Pending *reportRequest           `json:"pending"`
	Queued  map[string]traffic.Usage `json:"queued"`
}

func atomicStateFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".state-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		dir, err := os.Open(filepath.Dir(path))
		if err != nil {
			return err
		}
		defer dir.Close()
		return dir.Sync()
	}
	return nil
}
func (s *server) saveReportSpool(cfg config) error {
	if cfg.StateDir == "" {
		return nil
	}
	raw, err := json.Marshal(s.reportState)
	if err != nil {
		return err
	}
	return atomicStateFile(filepath.Join(cfg.StateDir, "traffic-spool.json"), raw)
}
func (s *server) pushReport(ctx context.Context, client *http.Client, cfg config) error {
	s.reportMu.Lock()
	defer s.reportMu.Unlock()
	if !s.reportLoaded {
		if cfg.StateDir != "" {
			raw, err := os.ReadFile(filepath.Join(cfg.StateDir, "traffic-spool.json"))
			if err == nil {
				if err = json.Unmarshal(raw, &s.reportState); err != nil {
					return err
				}
			} else if !os.IsNotExist(err) {
				return err
			}
		}
		s.reportLoaded = true
	}
	if s.reportState.Queued == nil {
		s.reportState.Queued = map[string]traffic.Usage{}
	}
	for name, u := range s.traffic.Drain() {
		v := s.reportState.Queued[name]
		v.Up += u.Up
		v.Down += u.Down
		s.reportState.Queued[name] = v
	}
	if s.reportState.Pending == nil {
		s.mu.Lock()
		running := s.node != nil
		s.mu.Unlock()
		sample := hostmetrics.Collect(running, singBoxVersion)
		sample.Tunnels = s.tunnelReport(sample.Tunnels)
		sample.ClientConnections = s.clientConnections()
		users := make([]reportUserUsage, 0, len(s.reportState.Queued))
		for name, u := range s.reportState.Queued {
			users = append(users, reportUserUsage{Username: name, Uplink: u.Up, Downlink: u.Down})
		}
		s.reportState.Pending = &reportRequest{ReportID: uuid.NewString(), Users: users, Host: sample}
		s.reportState.Queued = map[string]traffic.Usage{}
	}
	if err := s.saveReportSpool(cfg); err != nil {
		return err
	}
	body, err := json.Marshal(s.reportState.Pending)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.PanelURL+"/api/internal/node-report", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.ReportSecret)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("panel returned %d", resp.StatusCode)
	}
	s.reportState.Pending = nil
	return s.saveReportSpool(cfg)
}
func (s *server) configLoop(ctx context.Context, cfg config) {
	client := &http.Client{Timeout: 2 * time.Minute}
	ticker := time.NewTicker(cfg.ReportInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.pullOnce(ctx, client, cfg)
		}
	}
}
