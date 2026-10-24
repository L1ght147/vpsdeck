// Command vpsdeck is both the monitoring agent and the hub.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	_ "time/tzdata" // timezones work on minimal systems without /usr/share/zoneinfo

	"github.com/L1ght147/vpsdeck/internal/agent"
	"github.com/L1ght147/vpsdeck/internal/hub"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `vpsdeck — server status board for Telegram

Usage:
  vpsdeck agent [-config PATH] [-once] [-print] [-check]
  vpsdeck hub [-config PATH] [-dev]
  vpsdeck hub add-server -name NAME [-config PATH] [-print command|token]
  vpsdeck hub fingerprint [-config PATH]
  vpsdeck version
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "agent":
		err = runAgent(os.Args[2:])
	case "hub":
		switch {
		case len(os.Args) > 2 && os.Args[2] == "add-server":
			err = runAddServer(os.Args[3:])
		case len(os.Args) > 2 && os.Args[2] == "fingerprint":
			err = runFingerprint(os.Args[3:])
		default:
			err = runHub(os.Args[2:])
		}
	case "version", "-v", "--version":
		fmt.Println(version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

func runAgent(args []string) error {
	fs := flag.NewFlagSet("agent", flag.ExitOnError)
	cfgPath := fs.String("config", agent.DefaultConfigPath, "config file")
	once := fs.Bool("once", false, "send a single report and exit")
	print := fs.Bool("print", false, "print the report as JSON instead of sending it")
	_ = fs.Parse(args)

	ctx, cancel := signalContext()
	defer cancel()

	cfg, err := agent.LoadConfig(*cfgPath)
	if err != nil {
		if !*print {
			return err
		}
		cfg = &agent.Config{} // printing works without a config
	}
	a := agent.New(cfg, version)

	if *print {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(a.Collect(ctx))
	}
	if *once {
		if err := cfg.Validate(); err != nil {
			return err
		}
		return a.Send(ctx, a.Collect(ctx))
	}
	return a.Run(ctx)
}

func runHub(args []string) error {
	fs := flag.NewFlagSet("hub", flag.ExitOnError)
	cfgPath := fs.String("config", hub.DefaultConfigPath, "config file")
	dev := fs.Bool("dev", false, "local development: run without the Telegram bot")
	_ = fs.Parse(args)

	var cfg *hub.Config
	if *dev && !flagSet(fs, "config") {
		cfg = hub.DevConfig()
	} else {
		var err error
		if cfg, err = hub.LoadConfig(*cfgPath); err != nil {
			return err
		}
	}

	store, err := hub.OpenStore(cfg.DBPath)
	if err != nil {
		return err
	}
	defer store.Close()

	ctx, cancel := signalContext()
	defer cancel()
	slog.Info("starting hub", "version", version)
	return hub.New(cfg, store, hub.Options{Version: version, Dev: *dev}).Run(ctx)
}

func runAddServer(args []string) error {
	fs := flag.NewFlagSet("add-server", flag.ExitOnError)
	cfgPath := fs.String("config", hub.DefaultConfigPath, "config file")
	name := fs.String("name", "", "server name")
	print := fs.String("print", "command", "what to print: command or token")
	_ = fs.Parse(args)

	cfg, err := hub.LoadConfig(*cfgPath)
	if err != nil {
		return err
	}
	if !cfg.TLSDisable {
		// The install command carries the certificate fingerprint, so make sure it exists.
		if _, err := hub.EnsureCert(cfg.TLSCert, cfg.TLSKey); err != nil {
			return err
		}
	}
	store, err := hub.OpenStore(cfg.DBPath)
	if err != nil {
		return err
	}
	defer store.Close()

	srv, tok, err := store.AddServer(hub.SanitizeName(*name))
	if err != nil {
		return err
	}
	if *print == "token" {
		fmt.Println(tok)
		return nil
	}
	fmt.Println(hub.New(cfg, store, hub.Options{Version: version}).InstallCommand(tok, srv.Name))
	return nil
}

func runFingerprint(args []string) error {
	fs := flag.NewFlagSet("fingerprint", flag.ExitOnError)
	cfgPath := fs.String("config", hub.DefaultConfigPath, "config file")
	_ = fs.Parse(args)

	cfg, err := hub.LoadConfig(*cfgPath)
	if err != nil {
		return err
	}
	if cfg.TLSDisable {
		return fmt.Errorf("tls_disable is set: the hub has no certificate of its own")
	}
	cert, err := hub.EnsureCert(cfg.TLSCert, cfg.TLSKey)
	if err != nil {
		return err
	}
	fmt.Println(hub.Fingerprint(cert.Certificate[0]))
	return nil
}

func flagSet(fs *flag.FlagSet, name string) bool {
	found := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}
