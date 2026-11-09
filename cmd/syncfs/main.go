// Command syncfs is the SyncFS binary entry point (M4-03).
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/template"
	"time"

	"github.com/hanwen/go-fuse/v2/fuse"
	"github.com/mohamedhabas11/syncfs/internal/client"
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
		fmt.Fprintln(os.Stderr, "usage: syncfs admin <bootstrap|status|volume|member|test-file> ...")
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
	case "test-file":
		return runTestFile(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown admin subcommand: %s\n", args[0])
		return 2
	}
}

func runTestFile(args []string) int {
	fs := flag.NewFlagSet("syncfs admin test-file", flag.ContinueOnError)
	endpoint := fs.String("endpoint", "", "daemon HTTP endpoint (e.g. http://127.0.0.1:18081)")
	name := fs.String("name", "hello.txt", "file name")
	data := fs.String("data", "hello-syncfs", "file data to write durably")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *endpoint == "" {
		fmt.Fprintln(os.Stderr, "test-file requires --endpoint")
		return 2
	}
	w := client.NewWireSyncer(*endpoint)
	if err := w.OnWrite(*name, 0, []byte(*data)); err != nil {
		fmt.Fprintf(os.Stderr, "test-file write: %v\n", err)
		return 1
	}
	if err := w.OnFsync(*name); err != nil {
		fmt.Fprintf(os.Stderr, "test-file fsync: %v\n", err)
		return 1
	}
	fmt.Fprintf(os.Stdout, "test-file %s wrote %d bytes durably via %s\n", *name, len(*data), *endpoint)
	return 0
}

func runBootstrap(args []string) int {
	fs := flag.NewFlagSet("syncfs admin bootstrap", flag.ContinueOnError)
	clusterID := fs.String("cluster-id", "", "new cluster ID")
	out := fs.String("out", "", "fleet output directory (ca.pem + node-{1,2,3}.json/.pem/.key)")
	peers := fs.String("peers", "127.0.0.1:5001,127.0.0.1:5002,127.0.0.1:5003", "comma-separated 3 peer QUIC addrs")
	httpAddrs := fs.String("http-addrs", "127.0.0.1:18081,127.0.0.1:18082,127.0.0.1:18083", "comma-separated 3 HTTP listen addrs")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *clusterID == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "bootstrap requires --cluster-id and --out")
		return 2
	}
	addrs := strings.Split(*peers, ",")
	httpArr := strings.Split(*httpAddrs, ",")
	if len(addrs) != 3 || len(httpArr) != 3 {
		fmt.Fprintln(os.Stderr, "bootstrap requires exactly 3 peer and http addrs")
		return 2
	}
	if err := daemon.GenerateFleet(*clusterID, *out, addrs, httpArr, nil); err != nil {
		fmt.Fprintf(os.Stderr, "bootstrap: %v\n", err)
		return 1
	}
	fmt.Fprintf(os.Stdout, "bootstrap: cluster %s fleet -> %s (ca.pem + 3 node configs)\n", *clusterID, *out)
	return 0
}

func runStatus(args []string) int {
	fs := flag.NewFlagSet("syncfs admin status", flag.ContinueOnError)
	endpoint := fs.String("endpoint", "", "storage node HTTP endpoint (e.g. 127.0.0.1:18081)")
	format := fs.String("format", "text", "output format (text|json)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *endpoint == "" {
		fmt.Fprintln(os.Stderr, "status requires --endpoint")
		return 2
	}
	// Probe liveness + readiness + metrics. Unreachable endpoint is exit 1,
	// not a stub print.
	client := &http.Client{Timeout: 5 * time.Second}
	health, hErr := client.Get("http://" + *endpoint + "/healthz")
	ready, rErr := client.Get("http://" + *endpoint + "/readyz")
	if hErr != nil {
		fmt.Fprintf(os.Stderr, "status %s: unreachable: %v\n", *endpoint, hErr)
		return 1
	}
	defer health.Body.Close()
	rCode := 0
	if rErr == nil {
		rCode = ready.StatusCode
		ready.Body.Close()
	}
	if *format == "json" {
		fmt.Fprintf(os.Stdout, `{"endpoint":%q,"healthz":%d,"readyz":%d}`+"\n", *endpoint, health.StatusCode, rCode)
	} else {
		fmt.Fprintf(os.Stdout, "status %s: healthz=%d readyz=%d\n", *endpoint, health.StatusCode, rCode)
	}
	if health.StatusCode != http.StatusOK {
		return 1
	}
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
	daemonAddr := fs.String("daemon", "", "daemon HTTP endpoint (e.g. http://127.0.0.1:18081); required for durable writes")
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
	if *daemonAddr == "" {
		fmt.Fprintln(os.Stderr, "mount requires --daemon (durable-write endpoint); without it writes would be silent (fail-closed)")
		return 2
	}
	srv, err := mountOnceWithDaemon(*volume, *target, *daemonAddr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mount --volume %s --target %s: %v\n", *volume, *target, err)
		return 1
	}
	srv.Wait()
	return 0
}

// mountOnce performs the FUSE mount (non-blocking; caller owns the server).
func mountOnce(volume, target string) (*fuse.Server, error) {
	return mountOnceWithDaemon(volume, target, "")
}

// mountOnceWithDaemon wires durability through the daemon when an endpoint
// is supplied; without one (legacy tests) the mount is strict fail-closed.
func mountOnceWithDaemon(volume, target, daemonAddr string) (*fuse.Server, error) {
	if _, err := os.Stat("/dev/fuse"); err != nil {
		return nil, fmt.Errorf("FUSE device unavailable: %w", err)
	}
	h := fusefs.NewStrictHandler(volume)
	if daemonAddr != "" {
		h.SetSyncer(client.NewWireSyncer(daemonAddr))
	}
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
