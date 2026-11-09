// Command syncfs is the SyncFS binary entry point (M4-03).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/template"

	"github.com/hanwen/go-fuse/v2/fuse"
	"github.com/mohamedhabas11/syncfs/internal/daemon"
	"github.com/mohamedhabas11/syncfs/internal/fusefs"
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
	os.Exit(run(os.Args[1:]))
}

// run is the testable entry point; it returns an exit code.
func run(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: syncfs <admin|daemon|mount> ...")
		return 2
	}
	switch args[0] {
	case "admin":
		return runAdmin(args[1:])
	case "daemon":
		return runDaemon(args[1:])
	case "mount":
		return runMount(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", args[0])
		return 2
	}
}

func runAdmin(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: syncfs admin <bootstrap|status|volume|member> ...")
		return 2
	}
	switch args[0] {
	case "bootstrap":
		return runBootstrap(args[1:])
	case "status":
		return runStatus(args[1:])
	case "volume":
		return runVolume(args[1:])
	case "member":
		return runMember(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown admin subcommand: %s\n", args[0])
		return 2
	}
}

func runBootstrap(args []string) int {
	fs := flag.NewFlagSet("syncfs admin bootstrap", flag.ContinueOnError)
	clusterID := fs.String("cluster-id", "", "new cluster ID")
	out := fs.String("out", "", "output config path")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *clusterID == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "bootstrap requires --cluster-id and --out")
		return 2
	}
	// The bootstrap file is the enrollment root: without a quorum this
	// cannot create a cluster; the control plane performs the write.
	fmt.Fprintf(os.Stdout, "bootstrap: cluster %s -> %s (control plane applies)\n", *clusterID, *out)
	return 0
}

func runStatus(args []string) int {
	fs := flag.NewFlagSet("syncfs admin status", flag.ContinueOnError)
	endpoint := fs.String("endpoint", "", "storage node endpoint")
	format := fs.String("format", "text", "output format (text|json)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *endpoint == "" {
		fmt.Fprintln(os.Stderr, "status requires --endpoint")
		return 2
	}
	fmt.Fprintf(os.Stdout, "status: %s (format %s)\n", *endpoint, *format)
	return 0
}

func runVolume(args []string) int {
	fs := flag.NewFlagSet("syncfs admin volume", flag.ContinueOnError)
	volume := fs.String("volume", "", "volume name")
	op := fs.String("op", "status", "volume op (status|create|describe)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *volume == "" {
		fmt.Fprintln(os.Stderr, "volume requires --volume")
		return 2
	}
	fmt.Fprintf(os.Stdout, "volume %s op %s\n", *volume, *op)
	return 0
}

func runMember(args []string) int {
	fs := flag.NewFlagSet("syncfs admin member", flag.ContinueOnError)
	node := fs.String("node", "", "node id")
	op := fs.String("op", "status", "member op (status|add-learner|promote|remove)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *node == "" {
		fmt.Fprintln(os.Stderr, "member requires --node")
		return 2
	}
	fmt.Fprintf(os.Stdout, "member %s op %s\n", *node, *op)
	return 0
}

func runDaemon(args []string) int {
	fs := flag.NewFlagSet("syncfs daemon", flag.ContinueOnError)
	config := fs.String("config", "", "daemon config path")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *config == "" {
		fmt.Fprintln(os.Stderr, "daemon requires --config")
		return 2
	}
	// Recovery-first: New() restores WAL/chunk/metadata from disk and
	// validates identity + topology before any listener is opened.
	cfg, err := daemon.LoadConfig(*config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "daemon config invalid: %v\n", err)
		return 1
	}
	d, err := daemon.New(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "daemon: %v\n", err)
		return 1
	}
	defer d.Close()
	ctx, cancel := signalContext()
	defer cancel()
	if err := d.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "daemon: %v\n", err)
		return 1
	}
	return 0
}

func signalContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case <-ch:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

func runMount(args []string) int {
	fs := flag.NewFlagSet("syncfs mount", flag.ContinueOnError)
	volume := fs.String("volume", "", "volume name")
	target := fs.String("target", "", "mount target directory")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *volume == "" || *target == "" {
		fmt.Fprintln(os.Stderr, "mount requires --volume and --target")
		return 2
	}
	info, err := os.Stat(*target)
	if err != nil || !info.IsDir() {
		fmt.Fprintf(os.Stderr, "mount target must be an existing directory: %v\n", err)
		return 1
	}
	srv, err := mountOnce(*volume, *target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mount --volume %s --target %s: %v\n", *volume, *target, err)
		return 1
	}
	srv.Wait()
	return 0
}

// mountOnce performs the FUSE mount (non-blocking; caller owns the server).
func mountOnce(volume, target string) (*fuse.Server, error) {
	if _, err := os.Stat("/dev/fuse"); err != nil {
		return nil, fmt.Errorf("FUSE device unavailable: %w", err)
	}
	h := fusefs.NewStrictHandler(volume)
	return fusefs.KernelMount(target, h)
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
