package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/disksing/project-incubator/agenthub/internal/api"
	"github.com/disksing/project-incubator/agenthub/internal/client"
	"github.com/disksing/project-incubator/agenthub/internal/config"
	"github.com/disksing/project-incubator/agenthub/internal/daemon"
	"github.com/disksing/project-incubator/agenthub/internal/paths"
	"github.com/disksing/project-incubator/agenthub/internal/runtime"
	"github.com/disksing/project-incubator/agenthub/internal/session"
)

const version = "0.1.0-dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "agenthub:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: agenthub <serve|status|agents|run|chat|session>")
	}
	switch args[0] {
	case "serve":
		return runServe(args[1:])
	case "status":
		return runStatus(args[1:])
	case "agents":
		return runAgents(args[1:])
	case "run":
		return runOneShot(args[1:])
	case "chat":
		return runChat(args[1:])
	case "session":
		return runSession(args[1:])
	case "version", "--version", "-version":
		fmt.Println(version)
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runServe(args []string) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	address := flags.String("addr", "127.0.0.1:4646", "loopback address")
	webDir := flags.String("web-dir", "", "built Web UI directory (defaults to ./frontend/dist/client when present)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: agenthub serve [--addr 127.0.0.1:4646]")
	}
	if err := api.ValidateLoopbackAddress(*address); err != nil {
		return err
	}
	resolved, err := paths.Resolve()
	if err != nil {
		return err
	}
	if err := resolved.Ensure(); err != nil {
		return err
	}
	lock, err := daemon.AcquireLock(resolved.LockFile)
	if err != nil {
		return err
	}
	defer lock.Release()

	store, err := session.Open(resolved.SessionsDir)
	if err != nil {
		return err
	}
	cfg, err := config.Load(resolved.ConfigFile)
	if err != nil {
		return err
	}
	manager := runtime.New(store, cfg)
	defer manager.Close()
	if *webDir == "" {
		if absolute, statErr := filepath.Abs(filepath.Join("frontend", "dist", "client")); statErr == nil {
			if info, statErr := os.Stat(absolute); statErr == nil && info.IsDir() {
				*webDir = absolute
			}
		}
	}
	listener, err := net.Listen("tcp", *address)
	if err != nil {
		return err
	}
	defer listener.Close()

	startedAt := time.Now().UTC()
	endpoint := "http://" + listener.Addr().String()
	if err := daemon.WriteState(resolved.ServerFile, daemon.State{
		PID:       os.Getpid(),
		Endpoint:  endpoint,
		StartedAt: startedAt,
	}); err != nil {
		return err
	}
	defer os.Remove(resolved.ServerFile)

	httpServer := &http.Server{
		Handler: api.New(store, version, startedAt, api.Dependencies{
			Runtime: manager, ConfigPath: resolved.ConfigFile, WebDir: *webDir,
		}).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       75 * time.Second,
	}
	serverErrors := make(chan error, 1)
	go func() {
		err := httpServer.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
			return
		}
		serverErrors <- nil
	}()
	fmt.Printf("AgentHub %s listening on %s\n", version, endpoint)

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	select {
	case err := <-serverErrors:
		return err
	case <-signals:
		manager.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(ctx); err != nil {
			return err
		}
		return <-serverErrors
	}
}

func runStatus(args []string) error {
	if len(args) != 0 {
		return errors.New("usage: agenthub status")
	}
	apiClient, err := client.Discover()
	if err != nil {
		return err
	}
	status, err := apiClient.Status()
	if err != nil {
		return err
	}
	return printJSON(status)
}

func runAgents(args []string) error {
	if len(args) != 0 {
		return errors.New("usage: agenthub agents")
	}
	apiClient, err := client.Discover()
	if err != nil {
		return err
	}
	value, err := apiClient.Agents()
	if err != nil {
		return err
	}
	return printJSON(value)
}

func runOneShot(args []string) error {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	var tags stringList
	cwd := flags.String("cwd", ".", "working directory")
	title := flags.String("title", "", "session title")
	agentID := flags.String("agent", "", "explicit agent id")
	flags.Var(&tags, "tag", "agent profile/tag (repeatable)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	message := strings.TrimSpace(strings.Join(flags.Args(), " "))
	if message == "" {
		return errors.New("usage: agenthub run [--cwd .] [--agent id | --tag key] <message>")
	}
	if *agentID != "" && len(tags) > 0 {
		return errors.New("--agent and --tag are mutually exclusive")
	}
	absolute, err := filepath.Abs(*cwd)
	if err != nil {
		return err
	}
	apiClient, err := client.Discover()
	if err != nil {
		return err
	}
	value, err := apiClient.CreateSessionWithMessage(*title, absolute, *agentID, tags, message)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "session %s (%s)\n", value.ID, value.AgentID)
	return printUntilTurnEnds(apiClient, value.ID, 0)
}

func runChat(args []string) error {
	flags := flag.NewFlagSet("chat", flag.ContinueOnError)
	var tags stringList
	cwd := flags.String("cwd", ".", "working directory")
	title := flags.String("title", "", "session title")
	agentID := flags.String("agent", "", "explicit agent id")
	sessionID := flags.String("session", "", "attach existing session")
	flags.Var(&tags, "tag", "agent profile/tag (repeatable)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: agenthub chat [--session id | --cwd . --agent id]")
	}
	apiClient, err := client.Discover()
	if err != nil {
		return err
	}
	id := *sessionID
	if id == "" {
		absolute, err := filepath.Abs(*cwd)
		if err != nil {
			return err
		}
		value, err := apiClient.CreateSession(*title, absolute, *agentID, tags)
		if err != nil {
			return err
		}
		id = value.ID
	}
	fmt.Fprintf(os.Stderr, "attached %s; /quit exits, /stop stops provider, /interrupt cancels turn\n", id)
	reader := bufio.NewScanner(os.Stdin)
	attached, err := apiClient.GetSession(id)
	if err != nil {
		return err
	}
	cursor := attached.LastEventID
	for {
		fmt.Fprint(os.Stderr, "> ")
		if !reader.Scan() {
			return reader.Err()
		}
		text := strings.TrimSpace(reader.Text())
		switch text {
		case "":
			continue
		case "/quit", "/exit":
			return nil
		case "/stop":
			_, err := apiClient.SessionAction(id, "stop")
			return err
		case "/interrupt":
			_, err := apiClient.SessionAction(id, "interrupt")
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
			}
			continue
		}
		if _, err := apiClient.SendMessage(id, text, false); err != nil {
			fmt.Fprintln(os.Stderr, err)
			continue
		}
		cursor, err = printTurn(apiClient, id, cursor)
		if err != nil {
			return err
		}
	}
}

func printUntilTurnEnds(apiClient *client.Client, id string, cursor int64) error {
	_, err := printTurn(apiClient, id, cursor)
	return err
}

func printTurn(apiClient *client.Client, id string, cursor int64) (int64, error) {
	for {
		events, err := apiClient.EventsAfter(id, cursor)
		if err != nil {
			return cursor, err
		}
		for _, event := range events {
			cursor = event.ID
			switch event.Type {
			case "message.assistant.delta":
				var data struct {
					Text string `json:"text"`
				}
				_ = json.Unmarshal(event.Data, &data)
				fmt.Print(data.Text)
			case "approval.requested":
				fmt.Fprintln(os.Stderr, "\napproval required; use the Web UI or approval API")
			case "provider.error":
				var data map[string]any
				_ = json.Unmarshal(event.Data, &data)
				if message, _ := data["message"].(string); message != "" {
					fmt.Fprintln(os.Stderr, "\nprovider:", message)
				}
			case "turn.completed":
				fmt.Println()
				return cursor, nil
			case "turn.failed", "turn.cancelled":
				return cursor, fmt.Errorf("turn ended with %s", event.Type)
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func runSession(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: agenthub session <create|list|show|events|attach|resume|stop|interrupt|approve|archive>")
	}
	switch args[0] {
	case "create":
		return runSessionCreate(args[1:])
	case "list":
		return runSessionList(args[1:])
	case "show":
		return runSessionShow(args[1:])
	case "attach":
		return runChat(append([]string{"--session"}, args[1:]...))
	case "events":
		if len(args) != 2 {
			return errors.New("usage: agenthub session events <session-id>")
		}
		apiClient, err := client.Discover()
		if err != nil {
			return err
		}
		events, err := apiClient.EventsAfter(args[1], 0)
		if err != nil {
			return err
		}
		return printJSON(map[string]any{"events": events})
	case "approve":
		return runSessionApprove(args[1:])
	case "archive":
		if len(args) != 2 {
			return errors.New("usage: agenthub session archive <session-id>")
		}
		apiClient, err := client.Discover()
		if err != nil {
			return err
		}
		value, err := apiClient.ArchiveSession(args[1])
		if err != nil {
			return err
		}
		return printJSON(value)
	case "resume", "stop", "interrupt":
		if len(args) != 2 {
			return fmt.Errorf("usage: agenthub session %s <session-id>", args[0])
		}
		apiClient, err := client.Discover()
		if err != nil {
			return err
		}
		value, err := apiClient.SessionAction(args[1], args[0])
		if err != nil {
			return err
		}
		return printJSON(value)
	default:
		return fmt.Errorf("unknown session command %q", args[0])
	}
}

func runSessionApprove(args []string) error {
	flags := flag.NewFlagSet("session approve", flag.ContinueOnError)
	decision := flags.String("decision", "accept", "accept, acceptForSession, decline, or cancel")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 2 {
		return errors.New("usage: agenthub session approve [--decision accept] <session-id> <approval-id>")
	}
	switch *decision {
	case "accept", "acceptForSession", "decline", "cancel":
	default:
		return fmt.Errorf("invalid decision %q", *decision)
	}
	apiClient, err := client.Discover()
	if err != nil {
		return err
	}
	value, err := apiClient.ResolveApproval(flags.Arg(0), flags.Arg(1), *decision)
	if err != nil {
		return err
	}
	return printJSON(value)
}

func runSessionCreate(args []string) error {
	flags := flag.NewFlagSet("session create", flag.ContinueOnError)
	var tags stringList
	title := flags.String("title", "", "session title")
	cwd := flags.String("cwd", ".", "working directory")
	agentID := flags.String("agent", "", "explicit agent id")
	flags.Var(&tags, "tag", "agent routing tag (repeatable)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: agenthub session create [--cwd .] [--title title] [--agent id | --tag tag...]")
	}
	if *agentID != "" && len(tags) > 0 {
		return errors.New("--agent and --tag are mutually exclusive")
	}
	absoluteCwd, err := filepath.Abs(*cwd)
	if err != nil {
		return err
	}
	apiClient, err := client.Discover()
	if err != nil {
		return err
	}
	value, err := apiClient.CreateSession(*title, absoluteCwd, *agentID, tags)
	if err != nil {
		return err
	}
	return printJSON(value)
}

func runSessionList(args []string) error {
	flags := flag.NewFlagSet("session list", flag.ContinueOnError)
	includeArchived := flags.Bool("all", false, "include archived sessions")
	jsonOutput := flags.Bool("json", false, "print JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: agenthub session list [--all] [--json]")
	}
	apiClient, err := client.Discover()
	if err != nil {
		return err
	}
	values, err := apiClient.ListSessions(*includeArchived)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return printJSON(map[string]any{"sessions": values})
	}
	writer := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(writer, "ID\tSTATE\tAGENT\tTITLE\tUPDATED")
	for _, value := range values {
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\n",
			value.ID,
			value.State,
			value.AgentID,
			value.Title,
			value.UpdatedAt.Local().Format(time.RFC3339),
		)
	}
	return writer.Flush()
}

func runSessionShow(args []string) error {
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		return errors.New("usage: agenthub session show <session-id>")
	}
	apiClient, err := client.Discover()
	if err != nil {
		return err
	}
	value, err := apiClient.GetSession(args[0])
	if err != nil {
		return err
	}
	return printJSON(value)
}

func printJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

type stringList []string

func (s *stringList) String() string {
	return strings.Join(*s, ",")
}

func (s *stringList) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("tag cannot be empty")
	}
	*s = append(*s, value)
	return nil
}
