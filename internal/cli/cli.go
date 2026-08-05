// Package cli implements DELTA's thin command-line entry points.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ferriskleier/delta/internal/apperror"
	"github.com/ferriskleier/delta/internal/config"
	deltamcp "github.com/ferriskleier/delta/internal/mcp"
	"github.com/ferriskleier/delta/internal/server"
	"github.com/ferriskleier/delta/internal/service"
	"github.com/ferriskleier/delta/internal/storage"
)

// OpenBrowser is the browser-opening seam used by `delta serve` during
// first-run setup. Production defaults to the platform browser opener;
// tests can replace it with a no-op.
var OpenBrowser = openBrowser

const initUsage = "usage: delta init --path <p> or delta init --open <p> --key-stdin"

// Run executes one CLI command. It returns errors to the executable wrapper
// so callers can choose their own output stream; init's successful stdout is
// therefore exactly the generated key and nothing else.
func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("command required; try delta init, delta serve, delta mcp, delta entry, or delta backup")
	}
	if args[0] == "--version" || args[0] == "version" {
		_, err := fmt.Fprintln(stdout, currentBuildMetadata().describe())
		return err
	}
	switch args[0] {
	case "init":
		return runInit(ctx, args[1:], stdin, stdout)
	case "serve":
		return runServe(ctx, args[1:], stdout)
	case "service":
		return runService(ctx, args[1:], stdout)
	case "mcp":
		return runMCP(ctx, args[1:], stdin, stdout)
	case "entry":
		return runEntry(ctx, args[1:], stdin, stdout)
	case "habit":
		return runHabit(ctx, args[1:], stdout)
	case "backup":
		return runBackup(ctx, args[1:], stdout)
	case "grid":
		return runGrid(ctx, args[1:], stdout)
	case "stats":
		return runStats(ctx, args[1:], stdout)
	case "search":
		return runSearch(ctx, args[1:], stdout)
	default:
		return fmt.Errorf("unknown command %q; try delta init, delta serve, delta service, delta mcp, delta entry, delta habit, delta grid, delta stats, delta search, or delta backup", args[0])
	}
}

func runMCP(ctx context.Context, args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) != 0 {
		return errors.New("usage: delta mcp")
	}
	return deltamcp.Run(ctx, stdin, stdout)
}

func runInit(ctx context.Context, args []string, stdin io.Reader, stdout io.Writer) error {
	flags := flag.NewFlagSet("delta init", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	pathFlag := flags.String("path", "", "create a new diary at this path")
	openFlag := flags.String("open", "", "adopt an existing diary at this path")
	keyStdin := flags.Bool("key-stdin", false, "read the existing diary key from stdin")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *pathFlag != "" && *openFlag != "" {
		return errors.New("delta init accepts either --path or --open, not both")
	}
	if *pathFlag == "" && *openFlag == "" && !*keyStdin {
		return errors.New(initUsage)
	}
	if *openFlag != "" && !*keyStdin {
		return errors.New("delta init --open requires --key-stdin")
	}
	if *openFlag == "" && *keyStdin {
		return errors.New("--key-stdin is only valid with --open")
	}
	if err := config.CheckAvailable(); err != nil {
		return err
	}

	path := *pathFlag
	create := *openFlag == ""
	if path == "" && create {
		var err error
		path, err = config.DefaultDatabasePath()
		if err != nil {
			return err
		}
	}
	if *openFlag != "" {
		path = *openFlag
	}
	var err error
	path, err = config.ExpandPath(path)
	if err != nil {
		return err
	}
	var key string
	if *openFlag != "" {
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("open existing diary: %w", err)
		}
		pasted, err := io.ReadAll(stdin)
		if err != nil {
			return fmt.Errorf("read key from stdin: %w", err)
		}
		key = storage.NormalizeKey(string(pasted))
		if err := storage.ValidateKey(key); err != nil {
			return err
		}
	} else {
		if err := server.EnsureCreatePath(path); err != nil {
			if apperror.Code(err) == apperror.CodeDatabaseExists {
				return fmt.Errorf("database already exists at %s; use --open to adopt it", path)
			}
			return err
		}
		key, err = config.NewKey()
		if err != nil {
			return err
		}
	}

	store, err := storage.Open(ctx, path, key)
	if err != nil {
		return err
	}
	if err := store.Close(); err != nil {
		return err
	}
	c, err := config.New(path, key)
	if err != nil {
		return err
	}
	if err := config.Save(c); err != nil {
		return err
	}
	if create {
		_, err := fmt.Fprintln(stdout, key)
		return err
	}
	return nil
}

func runServe(ctx context.Context, args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("delta serve", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	listenFlag := flags.String("listen", "127.0.0.1:7331", "localhost address to bind")
	if err := flags.Parse(args); err != nil {
		return err
	}
	address, err := localhostAddress(*listenFlag)
	if err != nil {
		return err
	}
	c, err := config.Load()
	if err != nil {
		if errors.Is(err, config.ErrNotFound) {
			return runSetupServe(ctx, address, stdout)
		}
		return err
	}
	store, err := storage.Open(ctx, c.DatabasePath, c.Key)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := storage.MigrateStore(ctx, store); err != nil {
		return err
	}

	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", address, err)
	}
	defer listener.Close()
	boundAddress := "http://" + listener.Addr().String()
	if err := config.UpdateAPIAddress(c, boundAddress); err != nil {
		return err
	}
	c.APIAddress = boundAddress
	svc := service.New(store)
	httpServer := &http.Server{Handler: server.NewHandler(svc, c.APIToken, server.WithSettingsConfig(c), server.WithVersion(currentBuildMetadata().version))}
	if _, err := fmt.Fprintf(stdout, "delta %s serving %s on http://%s\n", currentBuildMetadata().version, c.DatabasePath, listener.Addr().String()); err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()
	if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func runSetupServe(ctx context.Context, address string, stdout io.Writer) error {
	configPath, err := config.Path()
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", address, err)
	}
	defer listener.Close()
	boundAddress := "http://" + listener.Addr().String()
	var store *storage.Store
	defer func() {
		if store != nil {
			_ = store.Close()
		}
	}()

	setupHandler := server.NewSetupHandler(func(setupCtx context.Context, request server.SetupRequest) (server.SetupCompletion, error) {
		return completeSetup(setupCtx, request, boundAddress, &store)
	})
	httpServer := &http.Server{Handler: setupHandler}
	if _, err := fmt.Fprintf(stdout, "delta %s no config at %s — first run\nsetup: %s (opening your browser…)\n", Version, configPath, boundAddress); err != nil {
		return err
	}
	OpenBrowser(boundAddress)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()
	if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func completeSetup(ctx context.Context, request server.SetupRequest, boundAddress string, storeSlot **storage.Store) (server.SetupCompletion, error) {
	key := storage.NormalizeKey(request.Key)
	if err := storage.ValidateKey(key); err != nil {
		return server.SetupCompletion{}, err
	}
	if request.Door == "create" {
		if err := server.EnsureCreatePath(request.Path); err != nil {
			return server.SetupCompletion{}, err
		}
	}

	store, err := storage.Open(ctx, request.Path, key)
	if err != nil {
		return server.SetupCompletion{}, err
	}
	if err := storage.MigrateStore(ctx, store); err != nil {
		_ = store.Close()
		return server.SetupCompletion{}, err
	}
	c, err := config.New(request.Path, key)
	if err != nil {
		_ = store.Close()
		return server.SetupCompletion{}, err
	}
	c.APIAddress = boundAddress
	if err := config.Save(c); err != nil {
		_ = store.Close()
		return server.SetupCompletion{}, err
	}

	svc := service.New(store)
	entries, err := svc.ListEntries(ctx, "", "")
	if err != nil {
		_ = store.Close()
		return server.SetupCompletion{}, err
	}
	configPath, err := config.Path()
	if err != nil {
		_ = store.Close()
		return server.SetupCompletion{}, err
	}
	done := server.SetupDone{
		Door:         request.Door,
		DatabasePath: c.DatabasePath,
		ConfigPath:   configPath,
		APIToken:     c.APIToken,
		EntryCount:   len(entries),
	}
	if len(entries) > 0 {
		done.FirstDate = entries[0].Date
		done.LastDate = entries[len(entries)-1].Date
	}
	*storeSlot = store
	return server.SetupCompletion{Done: done, Handler: server.NewHandler(svc, c.APIToken, server.WithSettingsConfig(c), server.WithVersion(currentBuildMetadata().version))}, nil
}

func openBrowser(address string) {
	command := "xdg-open"
	if runtime.GOOS == "darwin" {
		command = "open"
	}
	cmd := exec.Command(command, address)
	if err := cmd.Start(); err != nil {
		return
	}
	_ = cmd.Process.Release()
}

func localhostAddress(address string) (string, error) {
	if address == "" {
		return "127.0.0.1:7331", nil
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "", fmt.Errorf("invalid listen address %q: %w", address, err)
	}
	if host == "" {
		host = "127.0.0.1"
	}
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1":
		return net.JoinHostPort(host, port), nil
	default:
		return "", fmt.Errorf("delta serve only accepts localhost addresses, got %q", host)
	}
}

func FormatError(err error) string {
	if err == nil {
		return ""
	}
	if code := apperror.Code(err); code != apperror.CodeInternalError {
		return code + ": " + apperror.Message(err)
	}
	return err.Error()
}

func IsNoConfig(err error) bool { return errors.Is(err, config.ErrNotFound) }

func DefaultConfigPath() string {
	path, _ := config.Path()
	return filepath.Clean(path)
}
