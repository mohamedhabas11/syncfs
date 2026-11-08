# CLI and config reference

`syncfs` subcommands (M4-03, stubs until the control plane lands):

| Command | Args | Behavior today |
| --- | --- | --- |
| `syncfs admin` | subcommands | prints capability stub (control plane wiring is M4-03 remainder) |
| `syncfs daemon` | `--config <path>` | prints stub; units: `deploy/systemd/syncfs-daemon.service` |
| `syncfs mount` | `--volume <name> --target <path>` | prints stub; units: `deploy/systemd/syncfs-mount.service` |

Rationale and full flows: `docs/operating-model.md` (bootstrap + recovery).
