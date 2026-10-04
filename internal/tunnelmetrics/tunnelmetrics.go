// Package tunnelmetrics periodically SSHes into a tunnel's relay box (reusing
// the same stored credentials internal/tunnelprovision already provisioned
// it with - no new secret, no agent to deploy) and reads CPU/RAM/disk and the
// GRE interface's cumulative byte counters, so the Tunnels dashboard can show
// more than just up/down. Unlike internal/relayhealth (a bare TCP dial,
// works against any box with no login) this requires SSH access, so it only
// ever runs against rows in the tunnels table - a relay added purely for
// monitoring has no credentials to use.
package tunnelmetrics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/legendary1205/rapido-go/internal/cache"
	"github.com/legendary1205/rapido-go/internal/sshexec"
)

// Metrics is one relay's resource snapshot. CPUPercent is measured over a
// 1-second window on the box itself (two /proc/stat reads a second apart,
// in the same SSH round trip) - a real sample, not a load-average proxy.
// RxBytes/TxBytes are the GRE interface's cumulative counters since the
// box's last reboot (there is no separate "since this tunnel was created"
// counter at the kernel level), which is the simplest real measure of how
// much this tunnel has actually carried without touching frps's own config
// (no webServer/dashboard needs enabling on every relay for this).
type Metrics struct {
	CPUPercent  float64   `json:"cpu_percent"`
	MemUsedMB   int64     `json:"mem_used_mb"`
	MemTotalMB  int64     `json:"mem_total_mb"`
	DiskUsedGB  float64   `json:"disk_used_gb"`
	DiskTotalGB float64   `json:"disk_total_gb"`
	RxBytes     int64     `json:"rx_bytes"`
	TxBytes     int64     `json:"tx_bytes"`
	CheckedAt   time.Time `json:"checked_at"`
}

// stored is what actually goes into Redis - Metrics plus the tunnel id it
// belongs to and an optional collection error, mirroring relayhealth's own
// Status shape (id + verdict + error, one JSON array).
type stored struct {
	TunnelID int32   `json:"tunnel_id"`
	Metrics  Metrics `json:"metrics"`
	Error    string  `json:"error,omitempty"`
}

const redisKey = "tunnelmetrics:status"

// redisTTL is a little over 2x the collector's own default interval, same
// reasoning as relayhealth's TTL: if the backend singleton dies, the
// dashboard should stop showing numbers rather than serve stale ones forever.
const redisTTL = 3 * time.Minute

func publish(ctx context.Context, c *cache.Client, rows []stored) error {
	raw, err := json.Marshal(rows)
	if err != nil {
		return err
	}
	return c.Set(ctx, redisKey, string(raw), redisTTL)
}

// ReadMetrics reads back the last-published snapshot, keyed by tunnel id. A
// tunnel with no entry (nothing published yet, a collection error, or the
// key expired) is simply absent - the caller decides what to show for that.
func ReadMetrics(ctx context.Context, c *cache.Client) (map[int32]Metrics, error) {
	raw, err := c.Get(ctx, redisKey)
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var rows []stored
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		return nil, err
	}
	out := make(map[int32]Metrics, len(rows))
	for _, r := range rows {
		if r.Error != "" {
			continue
		}
		out[r.TunnelID] = r.Metrics
	}
	return out, nil
}

// Target is one tunnel to collect metrics for - just enough to dial its
// relay and know which interface's counters to read.
type Target struct {
	TunnelID         int32
	RelayHost        string
	RelaySSHPort     int32
	RelaySSHUser     string
	RelaySSHPassword string
	InterfaceName    string
}

// Lister returns the tunnels to collect from, called fresh every round -
// mirrors relayhealth.Lister.
type Lister func(ctx context.Context) ([]Target, error)

// script gathers everything in one SSH round trip: a 1-second CPU sample,
// memory, disk, and the GRE interface's byte counters, each on its own
// line with a fixed prefix so parseOutput doesn't need to guess at order.
const script = `
read _ a1 b1 c1 i1 _ < /proc/stat
sleep 1
read _ a2 b2 c2 i2 _ < /proc/stat
dt=$(( (a2+b2+c2+i2) - (a1+b1+c1+i1) ))
di=$(( i2 - i1 ))
if [ "$dt" -gt 0 ]; then echo "CPU:$(( (100*(dt-di))/dt ))"; else echo "CPU:0"; fi
free -m | awk '/^Mem:/ {print "MEM:"$3":"$2}'
df -BG / | awk 'NR==2 {gsub("G","",$2); gsub("G","",$3); print "DISK:"$3":"$2}'
echo "RX:$(cat /sys/class/net/%s/statistics/rx_bytes 2>/dev/null || echo 0)"
echo "TX:$(cat /sys/class/net/%s/statistics/tx_bytes 2>/dev/null || echo 0)"
`

func collect(ctx context.Context, relay *sshexec.Client, ifaceName string) (Metrics, error) {
	out, err := relay.Run(ctx, fmt.Sprintf(script, ifaceName, ifaceName))
	if err != nil {
		return Metrics{}, err
	}
	return parseOutput(out)
}

func parseOutput(out string) (Metrics, error) {
	var m Metrics
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "CPU:"):
			m.CPUPercent, _ = strconv.ParseFloat(strings.TrimPrefix(line, "CPU:"), 64)
		case strings.HasPrefix(line, "MEM:"):
			parts := strings.SplitN(strings.TrimPrefix(line, "MEM:"), ":", 2)
			if len(parts) == 2 {
				m.MemUsedMB, _ = strconv.ParseInt(parts[0], 10, 64)
				m.MemTotalMB, _ = strconv.ParseInt(parts[1], 10, 64)
			}
		case strings.HasPrefix(line, "DISK:"):
			parts := strings.SplitN(strings.TrimPrefix(line, "DISK:"), ":", 2)
			if len(parts) == 2 {
				m.DiskUsedGB, _ = strconv.ParseFloat(parts[0], 64)
				m.DiskTotalGB, _ = strconv.ParseFloat(parts[1], 64)
			}
		case strings.HasPrefix(line, "RX:"):
			m.RxBytes, _ = strconv.ParseInt(strings.TrimPrefix(line, "RX:"), 10, 64)
		case strings.HasPrefix(line, "TX:"):
			m.TxBytes, _ = strconv.ParseInt(strings.TrimPrefix(line, "TX:"), 10, 64)
		}
	}
	m.CheckedAt = time.Now()
	return m, nil
}

type Options struct {
	Lister      Lister
	Interval    time.Duration
	DialTimeout time.Duration
	Logger      *slog.Logger
	OnTick      func(rows []stored)
}

// Run collects every interval until ctx is done, concurrently across
// targets (one SSH dial + ~1s CPU sample each, so sequential would scale
// badly with fleet size). The first round runs immediately.
func Run(ctx context.Context, c *cache.Client, o Options) {
	if o.Interval <= 0 {
		o.Interval = 60 * time.Second
	}
	if o.DialTimeout <= 0 {
		o.DialTimeout = 10 * time.Second
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}

	tick := func() {
		targets, err := o.Lister(ctx)
		if err != nil {
			o.Logger.Warn("tunnelmetrics: could not list targets", "error", err)
			return
		}
		rows := make([]stored, len(targets))
		var wg sync.WaitGroup
		for i, t := range targets {
			wg.Add(1)
			go func(i int, t Target) {
				defer wg.Done()
				rows[i] = stored{TunnelID: t.TunnelID}
				dctx, cancel := context.WithTimeout(ctx, o.DialTimeout+2*time.Second)
				defer cancel()
				client, err := sshexec.Dial(dctx, sshexec.Config{
					Host: t.RelayHost, Port: t.RelaySSHPort, User: t.RelaySSHUser, Password: t.RelaySSHPassword,
				})
				if err != nil {
					rows[i].Error = err.Error()
					return
				}
				defer client.Close()
				m, err := collect(dctx, client, t.InterfaceName)
				if err != nil {
					rows[i].Error = err.Error()
					return
				}
				rows[i].Metrics = m
			}(i, t)
		}
		wg.Wait()

		if err := publish(ctx, c, rows); err != nil {
			o.Logger.Warn("tunnelmetrics: could not publish to redis", "error", err)
		}
		if o.OnTick != nil {
			o.OnTick(rows)
		}
	}

	tick()
	ticker := time.NewTicker(o.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tick()
		}
	}
}
