package cmd

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/fabhiantomaoludyo/gws-tui/internal/api"
	daemonpkg "github.com/fabhiantomaoludyo/gws-tui/internal/daemon"
	"github.com/fabhiantomaoludyo/gws-tui/internal/tui"
)

func runDaemon(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || isHelp(args[0]) {
		printDaemonUsage(stdout)
		return 0
	}
	cfg, err := tui.LoadConfig()
	if err != nil {
		fmt.Fprintf(stderr, "gws daemon: config: %v\n", err)
		return 3
	}
	switch args[0] {
	case "start":
		return runDaemonStart(args[1:], cfg, stdout, stderr)
	case "stop":
		return runDaemonStop(cfg, stdout, stderr)
	case "status":
		return runDaemonStatus(cfg, stdout, stderr)
	case "logs":
		return runDaemonLogs(cfg, stdout, stderr)
	case "restart":
		if code := runDaemonStop(cfg, stdout, stderr); code != 0 && code != 4 {
			return code
		}
		if err := daemonpkg.StartDetached(cfg.DaemonLog, "daemon", "start"); err != nil {
			fmt.Fprintf(stderr, "gws daemon restart: %v\n", err)
			return 5
		}
		fmt.Fprintf(stdout, "daemon restarting at %s\n", cfg.DaemonSocket)
		return 0
	default:
		fmt.Fprintf(stderr, "gws daemon: unknown subcommand %q\n", args[0])
		return 3
	}
}

func runDaemonStart(args []string, cfg tui.Config, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("gws daemon start", flag.ContinueOnError)
	flags.SetOutput(stderr)
	detach := flags.Bool("detach", false, "start daemon in the background")
	if err := flags.Parse(args); err != nil {
		return 3
	}
	if *detach {
		if err := daemonpkg.StartDetached(cfg.DaemonLog, "daemon", "start"); err != nil {
			fmt.Fprintf(stderr, "gws daemon start: %v\n", err)
			return 5
		}
		fmt.Fprintf(stdout, "daemon starting at %s\n", cfg.DaemonSocket)
		return 0
	}

	if err := tui.SetupLogging(cfg.DaemonLog); err != nil {
		fmt.Fprintf(stderr, "gws daemon start: logging: %v\n", err)
		return 3
	}
	lock, err := daemonpkg.AcquirePIDLock(cfg.DaemonPIDFile)
	if err != nil {
		fmt.Fprintf(stderr, "gws daemon start: %v\n", err)
		return 4
	}
	defer lock.Release()

	upstream, _ := findUpstreamGWS()
	client := api.NewDefaultClient(api.ClientOptions{
		UpstreamPath:  upstream,
		ForceFixture:  shouldUseFixtures() || upstream == "",
		FixtureReason: upstreamDescription(),
	})
	defer client.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	server := daemonpkg.NewServer(client, daemonOptions(cfg))
	fmt.Fprintf(stdout, "daemon listening on %s\n", cfg.DaemonSocket)
	if err := server.ListenAndServe(ctx); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(stderr, "gws daemon start: %v\n", err)
		return 5
	}
	return 0
}

func runDaemonStop(cfg tui.Config, stdout, stderr io.Writer) int {
	if err := daemonpkg.StopPID(cfg.DaemonPIDFile, 5*time.Second); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintln(stderr, "gws daemon stop: daemon is not running")
			return 4
		}
		fmt.Fprintf(stderr, "gws daemon stop: %v\n", err)
		return 5
	}
	fmt.Fprintln(stdout, "daemon stopped")
	return 0
}

func runDaemonStatus(cfg tui.Config, stdout, stderr io.Writer) int {
	client, err := api.NewRemoteClient(cfg.DaemonSocket)
	if err != nil {
		pid, pidErr := daemonpkg.ReadPID(cfg.DaemonPIDFile)
		if pidErr == nil && daemonpkg.ProcessRunning(pid) {
			fmt.Fprintf(stdout, "running: pid=%d socket=%s (ping failed: %v)\n", pid, cfg.DaemonSocket, err)
			return 1
		}
		fmt.Fprintf(stdout, "stopped: socket=%s pid_file=%s\n", cfg.DaemonSocket, cfg.DaemonPIDFile)
		return 4
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	status, err := client.DaemonStatus(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "gws daemon status: %v\n", err)
		return 5
	}
	fmt.Fprintf(stdout, "running: pid=%d socket=%s uptime=%s clients=%d protocol=%d\n",
		status.PID,
		status.SocketPath,
		(time.Duration(status.UptimeSeconds) * time.Second).String(),
		len(status.Clients),
		status.ProtocolVersion,
	)
	for _, client := range status.Clients {
		fmt.Fprintf(stdout, "- client=%d attached=%s topics=%s\n", client.ID, client.AttachedAt.Format(time.RFC3339), strings.Join(client.Topics, ","))
	}
	return 0
}

func runDaemonLogs(cfg tui.Config, stdout, stderr io.Writer) int {
	file, err := os.Open(cfg.DaemonLog)
	if err != nil {
		fmt.Fprintf(stderr, "gws daemon logs: %v\n", err)
		return 4
	}
	defer file.Close()
	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
		if len(lines) > 200 {
			lines = lines[1:]
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(stderr, "gws daemon logs: %v\n", err)
		return 5
	}
	for _, line := range lines {
		fmt.Fprintln(stdout, line)
	}
	return 0
}

func daemonOptions(cfg tui.Config) daemonpkg.Options {
	return daemonpkg.Options{
		SocketPath:      cfg.DaemonSocket,
		CachePath:       cfg.CachePath,
		DraftDir:        cfg.DraftDir,
		ImageCacheDir:   cfg.ImageCacheDir,
		NotifyDesktop:   cfg.NotifyDesktop,
		NotifySound:     cfg.NotifySound,
		NotifySoundFile: cfg.NotifySoundFile,
	}
}

func printDaemonUsage(w io.Writer) {
	fmt.Fprint(w, `gws daemon — background workspace backend

USAGE:
    gws daemon start [--detach]
    gws daemon stop
    gws daemon status
    gws daemon logs
    gws daemon restart
`)
}

func connectDaemonClient(cfg tui.Config) (*api.RemoteClient, *api.WorkspaceSnapshot, error) {
	client, err := api.NewRemoteClient(cfg.DaemonSocket)
	if err != nil && cfg.DaemonAutospawn {
		if startErr := daemonpkg.StartDetached(cfg.DaemonLog, "daemon", "start"); startErr != nil {
			return nil, nil, startErr
		}
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			time.Sleep(150 * time.Millisecond)
			client, err = api.NewRemoteClient(cfg.DaemonSocket)
			if err == nil {
				break
			}
		}
	}
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = client.ClientHello(ctx, os.Getpid(), os.Getenv("TTY"))
	snapshot, err := client.Snapshot(ctx)
	if err != nil {
		_ = client.Close()
		return nil, nil, err
	}
	if snapshot.ProtocolVersion != api.ProtocolVersion {
		_ = client.Close()
		return nil, nil, fmt.Errorf("daemon protocol version %d is incompatible with client protocol version %d; restart the daemon", snapshot.ProtocolVersion, api.ProtocolVersion)
	}
	return client, &snapshot, nil
}
