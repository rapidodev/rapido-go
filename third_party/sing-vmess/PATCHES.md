# Local changes to sing-vmess

Based on github.com/sagernet/sing-vmess v0.2.8-0.20250909125414-3aed155119a1. The upstream license and source notices stay in place.

- `vless/protocol.go`: an addon's length is checked against what the addons block holds before it is allocated. Upstream passes the peer's varint straight to `make` before the UUID is checked, so one unauthenticated connection could end the whole process.
- `vless/vision.go`: `NewVisionConn` is marked `//go:nocheckptr`. It changes nothing in a normal build; without it the test suite's `-race` build dies on the first Vision connection.

Compare these changes against upstream when upgrading the module. This module is selected by the root go.mod replace directive.
