# Config reference

Keys are read from the daemon config file passed via `--config`.
Generator: none (manual reference; see `cmd/syncfs/main.go` flag names).

| Key | Flag/file | Meaning |
| --- | --- | --- |
| `--config` | `syncfs daemon --config <path>` | Daemon config path. Must exist (else exit 1). Rendered into systemd via `RenderUnit`. |
| `--cluster-id` | `syncfs admin bootstrap --cluster-id` | New cluster UUID for enrollment bundles. |
| `--endpoint` | `syncfs admin status --endpoint` | Storage-node endpoint to probe. |
| `--volume` | `syncfs admin volume --volume`, `syncfs mount --volume` | Volume name to operate on / mount. |
| `--target` | `syncfs mount --target` | Existing mount target directory. |
| `--node` | `syncfs admin member --node` | Node ID for membership operations. |

Systemd templates: `deploy/systemd/syncfs-daemon.service`,
`deploy/systemd/syncfs-mount.service` (ExecStart embeds role + config).
