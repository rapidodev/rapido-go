# Local issue: security, reliability and 50k-account readiness

Branch: fix/reliability-security-scale, based on local dev at 1c23268.
This issue is local; no external publication or deployment is authorized.

Required work: bootstrap identity isolation; node certificate authorization; inbound access enforcement; selective connection revocation; durable idempotent traffic reports; atomic user operations; validated snapshot/apply/rollback; singleton-loss cancellation; compact configuration sync; notification and gateway queues; periodic policy jobs; bounded queries; restore exclusion; login and log privacy; frontend session isolation; SSH trust; installer ordering; CI and disposable integration/load tests.

Validation and outstanding work are recorded below as implementation proceeds. Never interpret skipped integration tests as passing.
