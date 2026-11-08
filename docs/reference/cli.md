# CLI and config reference

Generated from `cmd/syncfs/main.go`. Run `syncfs --help`-style usage via:
`syncfs admin <subcommand> --help` (flag package prints usage). Generator
command: none (hand-written reference kept in sync by `TestAdminCLIParsing`).

## Admin

| Command | Args | Behavior |
| --- | --- | --- |
| `syncfs admin bootstrap` | `--cluster-id <uuid> --out <path>` | Describes the bootstrap the control plane must apply. Missing flags → exit 2. |
| `syncfs admin status` | `--endpoint <host:port> [--format text\|json]` | Describes the status probe. Missing endpoint → exit 2. |
| `syncfs admin volume` | `--volume <name> [--op status\|create\|describe]` | Describes the volume operation. Missing `--volume` → exit 2. |
| `syncfs admin member` | `--node <id> [--op status\|add-learner\|promote\|remove]` | Describes the membership operation; joint promotion goes through `RequestJointPromotion`. Missing `--node` → exit 2. |

## Daemon / mount

| Command | Args | Behavior |
| --- | --- | --- |
| `syncfs daemon` | `--config <path>` | Requires an existing config file (else exit 1). Units: `deploy/systemd/syncfs-daemon.service`. |
| `syncfs mount` | `--volume <name> --target <dir>` | Requires an existing directory target (else exit 1). Units: `deploy/systemd/syncfs-mount.service`. |

Exit codes: 0 success, 1 missing file/dir or unreachable, 2 usage error.
The CLI validates and describes; Raft proposals are applied by the control
plane (see `docs/operating-model.md`).

Reference docs: `docs/architecture/index.md`, `docs/failure-semantics.md`.
