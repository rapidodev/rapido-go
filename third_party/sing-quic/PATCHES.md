# Local changes to sing-quic

Based on github.com/sagernet/sing-quic v0.7.0-beta.4. The upstream GPL-3.0-or-later license and source notices are preserved.

- Synchronize authentication table publication and lookups in Hysteria, Hysteria2 and TUIC so live user updates cannot race with authentication. TUIC reads its user and password tables under the same lock.
- Serialize authentication on an individual Hysteria2 or TUIC session. Hysteria2 publishes its session identity through an atomic flag after successful authentication.
- Remove rejected Hysteria passwords from authentication error messages.

Compare these changes against upstream when upgrading the module. This module is selected by the root go.mod replace directive and is included by the product Dockerfile's third_party copy.
