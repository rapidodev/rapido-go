# Local changes to sing-shadowsocks

Based on github.com/sagernet/sing-shadowsocks v0.2.8. The upstream license and source notices stay in place.

- `shadowaead/aead.go`: a chunk's announced length is checked against the read buffer before it is sliced. Upstream slices with it directly, so any user with a valid password could end the whole process, and a later chunk does so on the connection's copy goroutine, where nothing in this project could recover it.

Compare these changes against upstream when upgrading the module. This module is selected by the root go.mod replace directive.
