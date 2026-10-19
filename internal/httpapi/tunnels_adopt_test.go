package httpapi

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/legendary1205/rapido-go/internal/db/generated"
	"github.com/legendary1205/rapido-go/internal/relayhealth"
	"github.com/legendary1205/rapido-go/internal/tunneldiscover"
	"github.com/legendary1205/rapido-go/internal/tunnelmetrics"
)

func TestNormalizePortsSortsDedupesAndRejectsBadInput(t *testing.T) {
	got, err := normalizePorts([]int32{20005, 20001, 20005, 20110})
	if err != nil || !reflect.DeepEqual(got, []int32{20001, 20005, 20110}) {
		t.Errorf("normalizePorts = %v, %v", got, err)
	}
	for _, bad := range [][]int32{nil, {}, {0}, {-1}, {65536}, {20001, 70000}} {
		if _, err := normalizePorts(bad); err == nil {
			t.Errorf("normalizePorts(%v) accepted bad input", bad)
		}
	}
}

func TestSamePortsComparesAfterNormalizing(t *testing.T) {
	a, _ := normalizePorts([]int32{20005, 20001})
	b, _ := normalizePorts([]int32{20001, 20005, 20001})
	if !samePorts(a, b) {
		t.Error("the same set of ports in a different order must count as unchanged")
	}
	c, _ := normalizePorts([]int32{20001, 20006})
	if samePorts(a, c) {
		t.Error("different ports must count as changed")
	}
}

func TestDiscoverRequestNeedsAllThreeRelayFieldsToReadTheRelay(t *testing.T) {
	r := discoverRequest{RelayHost: "203.0.113.82", RelaySSHUser: "root"}
	if r.hasRelayLogin() {
		t.Error("a relay login without a password must not count")
	}
	r.RelaySSHPassword = "x"
	if !r.hasRelayLogin() {
		t.Error("host, user and password together are a relay login")
	}
}

func TestToTunnelDTOExposesDetectionButNeverThePasswords(t *testing.T) {
	details, _ := json.Marshal(tunneldiscover.Details{
		Discovered: true, Protocol: "frp over TCP via HAProxy (IPv6 with IPv4 backup)",
		RelayAddrs: []string{"2001:db8:3::fe4", "203.0.113.82"}, NodeNIC: "eth0", RelayNIC: "eth0",
	})
	row := generated.Tunnel{
		ID: 7, Name: "node2", Method: "frp_direct", NodeID: 2, RelayHost: "203.0.113.82", Ports: []int32{20001},
		NodeSshPort: 22, NodeSshUser: "root", NodeSshPassword: "SECRET-NODE", RelaySshPort: 22, RelaySshUser: "root", RelaySshPassword: "SECRET-RELAY",
		Managed: false, Details: details, Status: "active",
	}
	dto := toTunnelDTO(row, relayhealth.Status{}, false, tunnelmetrics.Metrics{}, false)
	if dto.Managed {
		t.Error("a detected tunnel must be reported as not managed")
	}
	if dto.Protocol == nil || *dto.Protocol != "frp over TCP via HAProxy (IPv6 with IPv4 backup)" {
		t.Errorf("protocol = %v", dto.Protocol)
	}
	if !reflect.DeepEqual(dto.RelayAddrs, []string{"2001:db8:3::fe4", "203.0.113.82"}) || dto.NodeNIC == nil || *dto.NodeNIC != "eth0" {
		t.Errorf("details not carried: %+v", dto)
	}
	if dto.NodeSSHUser != "root" || dto.RelaySSHPort != 22 {
		t.Errorf("non-secret logins missing: %+v", dto)
	}
	raw, _ := json.Marshal(dto)
	for _, secret := range []string{"SECRET-NODE", "SECRET-RELAY"} {
		if json.Valid(raw) && containsString(string(raw), secret) {
			t.Errorf("the API response leaks a stored password (%s)", secret)
		}
	}
}

func TestToTunnelDTOLabelsAPanelBuiltTunnelFromItsMethod(t *testing.T) {
	dto := toTunnelDTO(generated.Tunnel{Method: "wg_dnat", Managed: true, Details: []byte("{}")}, relayhealth.Status{}, false, tunnelmetrics.Metrics{}, false)
	if dto.Protocol == nil || *dto.Protocol != "WireGuard + kernel port forwarding" || !dto.Managed {
		t.Errorf("dto = %+v", dto)
	}
	if dto.RelayAddrs == nil {
		t.Error("relay_addrs must serialise as [], not null")
	}
}

func containsString(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
