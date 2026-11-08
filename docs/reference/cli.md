# CLI and config reference

Generated from `cmd/syncfs/main.go`. Run `syncfs --help`-style usage via:
`syncfs admin <subcommand> --help` (flag package prints usage). Generator
command: none (hand-written reference kept in sync by `TestAdminCLIParsing`).

All commands below are **stubs**: they validate flags/files/dirs and print
the control-plane action that *would* run. They never contact a daemon,
propose to Raft, write a config file, or mount. There is no
`syncfs admin pki` subcommand.

## Admin

| Command | Args | Behavior |
| --- | --- | --- |
| `syncfs admin bootstrap` | `--cluster-id <uuid> --out <path>` | STUB: prints `bootstrap: cluster <id> -> <path> (control plane applies)`. Missing flags → exit 2. Does not write `<path>`. |
| `syncfs admin status` | `--endpoint <host:port> [--format text\|json]` | STUB: prints `status: <endpoint> (format <f>)`. Missing endpoint → exit 2. Does not probe. |
| `syncfs admin volume` | `--volume <name> [--op status\|create\|describe]` | STUB: prints `volume <name> op <op>`. Missing `--volume` → exit 2. |
| `syncfs admin member` | `--node <id> [--op status\|add-learner\|promote\|remove]` | STUB: prints `member <id> op <op>`. Joint promotion itself goes through `RequestJointPromotion` in the control plane. Missing `--node` → exit 2. |

## Daemon / mount

| Command | Args | Behavior |
| --- | --- | --- |
| `syncfs daemon` | `--config <path>` | STUB: requires an existing config file (else exit 1) and prints `syncfs daemon --config <path>`; does not start any daemon. Units: `deploy/systemd/syncfs-daemon.service`. |
| `syncfs mount` | `--volume <name> --target <dir>` | STUB: requires an existing directory target (else exit 1) and prints `syncfs mount --volume <name> --target <dir>`; does not mount. Units: `deploy/systemd/syncfs-mount.service`. |

Exit codes: 0 success (after validation/print), 1 missing file/dir, 2
usage error. The CLI validates and describes; Raft proposals are applied
by the control plane (see `docs/operating-model.md`).

Reference docs: `docs/architecture/index.md`, `docs/failure-semantics.md`.
