// Command syncfs is the SyncFS binary entry point (M4-03).
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"text/template"
)

const unitTemplate = `[Unit]
Description=SyncFS {{.role}} service
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/bin/syncfs {{.role}} --config {{.config}}
Restart=on-failure
RestartSec=2

[Install]
WantedBy=multi-user.target
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: syncfs <admin|daemon|mount> ...")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "admin":
		runAdmin(os.Args[2:])
	case "daemon":
		runDaemon(os.Args[2:])
	case "mount":
		runMount(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", os.Args[1])
		os.Exit(2)
	}
}

func runAdmin(args []string) {
	fs := flag.NewFlagSet("syncfs admin", flag.ExitOnError)
	_ = fs.Parse(args)
	// TODO(human): wire to internal/admin commands once the control plane lands (M4-03 remainder)
	fmt.Fprintln(os.Stdout, "syncfs admin (stub — M4-03): see docs/reference/cli.md")
}

func runDaemon(args []string) {
	_ = flag.NewFlagSet("syncfs daemon", flag.ContinueOnError).Parse(args)
	fmt.Fprintln(os.Stdout, "syncfs daemon (stub — M4-03): see docs/reference/cli.md")
}

func runMount(args []string) {
	_ = flag.NewFlagSet("syncfs mount", flag.ContinueOnError).Parse(args)
	fmt.Fprintln(os.Stdout, "syncfs mount (stub — M4-03): see docs/reference/cli.md")
}

// RenderUnit renders a systemd unit for a role/config pair.
func RenderUnit(role, config string) (string, error) {
	t, err := template.New("unit").Parse(unitTemplate)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := t.Execute(&b, map[string]string{"role": role, "config": config}); err != nil {
		return "", err
	}
	return b.String(), nil
}

var _ = template.ExecError{}
