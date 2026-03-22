package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/cleancoders-studio/waggle/internal/mcp"
	"github.com/cleancoders-studio/waggle/internal/server"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "start":
		cmdStart()
	case "status":
		cmdStatus()
	case "task":
		if len(os.Args) < 3 {
			fmt.Println("Usage: waggle task <add|list|show|update|claim|done|rm> [args]")
			os.Exit(1)
		}
		cmdTask(os.Args[2], os.Args[3:])
	case "tasks":
		cmdTask("list", os.Args[2:])
	case "agents":
		cmdAgents()
	case "mcp":
		cmdMCP()
	case "connect":
		cmdConnect()
	case "watch":
		cmdWatch()
	case "msg":
		if len(os.Args) < 3 {
			fmt.Println("Usage: waggle msg <send|list> [args]")
			os.Exit(1)
		}
		cmdMsg(os.Args[2], os.Args[3:])
	case "stop":
		cmdStop()
	case "backup":
		cmdBackup()
	case "reset":
		cmdReset()
	case "version":
		fmt.Println("waggle v0.1.0")
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func cmdStart() {
	port := 4740
	for i, arg := range os.Args[2:] {
		if arg == "--port" && i+1 < len(os.Args[2:]) {
			fmt.Sscanf(os.Args[i+3], "%d", &port)
		}
	}

	srv, err := server.New(server.Config{Port: port})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()

	if err := srv.Start(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}

func cmdStatus() {
	resp, err := http.Get(baseURL() + "/health")
	if err != nil {
		fmt.Println("waggle server is not running")
		os.Exit(1)
	}
	defer resp.Body.Close()
	fmt.Println("waggle server is running")

	// Show agents
	agentResp, err := http.Get(baseURL() + "/api/agents")
	if err == nil {
		defer agentResp.Body.Close()
		var agents []map[string]any
		json.NewDecoder(agentResp.Body).Decode(&agents)
		if len(agents) > 0 {
			fmt.Printf("\nConnected agents: %d\n", len(agents))
			for _, a := range agents {
				fmt.Printf("  %s (%s) - %s\n", a["name"], a["type"], a["status"])
			}
		}
	}
}

func cmdTask(subcmd string, args []string) {
	switch subcmd {
	case "add":
		if len(args) == 0 {
			fmt.Println("Usage: waggle task add \"title\" [--priority high] [--tag backend]")
			os.Exit(1)
		}
		task := map[string]any{"title": args[0]}
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--priority":
				if i+1 < len(args) {
					task["priority"] = args[i+1]
					i++
				}
			case "--tag":
				if i+1 < len(args) {
					tags, ok := task["tags"].([]string)
					if !ok {
						tags = []string{}
					}
					tags = append(tags, args[i+1])
					task["tags"] = tags
					i++
				}
			case "--criteria":
				if i+1 < len(args) {
					criteria, ok := task["criteria"].([]string)
					if !ok {
						criteria = []string{}
					}
					criteria = append(criteria, args[i+1])
					task["criteria"] = criteria
					i++
				}
			case "--estimate":
				if i+1 < len(args) {
					task["estimate"] = args[i+1]
					i++
				}
			case "--parent":
				if i+1 < len(args) {
					task["parent_id"] = args[i+1]
					i++
				}
			case "--depends":
				if i+1 < len(args) {
					deps, ok := task["depends_on"].([]string)
					if !ok {
						deps = []string{}
					}
					deps = append(deps, args[i+1])
					task["depends_on"] = deps
					i++
				}
			}
		}
		body, _ := json.Marshal(task)
		resp, err := http.Post(baseURL()+"/api/tasks", "application/json", strings.NewReader(string(body)))
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()
		var result map[string]any
		json.NewDecoder(resp.Body).Decode(&result)
		if resp.StatusCode >= 400 {
			fmt.Fprintf(os.Stderr, "error: %v\n", result)
			os.Exit(1)
		}
		fmt.Printf("Created task %s: %s\n", result["id"], result["title"])

	case "list":
		url := baseURL() + "/api/tasks"
		params := []string{}
		for i := 0; i < len(args); i++ {
			switch args[i] {
			case "--status":
				if i+1 < len(args) {
					params = append(params, "status="+args[i+1])
					i++
				}
			case "--assignee":
				if i+1 < len(args) {
					params = append(params, "assignee="+args[i+1])
					i++
				}
			case "--priority":
				if i+1 < len(args) {
					params = append(params, "priority="+args[i+1])
					i++
				}
			}
		}
		if len(params) > 0 {
			url += "?" + strings.Join(params, "&")
		}
		resp, err := http.Get(url)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()
		var tasks []map[string]any
		json.NewDecoder(resp.Body).Decode(&tasks)
		if len(tasks) == 0 {
			fmt.Println("No tasks found")
			return
		}
		for _, t := range tasks {
			assignee := ""
			if a, ok := t["assignee"].(string); ok && a != "" {
				assignee = " -> " + a
			}
			fmt.Printf("  [%s] %s %-12s %s%s\n", t["id"], statusIcon(t["status"]), t["priority"], t["title"], assignee)
		}

	case "show":
		if len(args) == 0 {
			fmt.Println("Usage: waggle task show <id>")
			os.Exit(1)
		}
		resp, err := http.Get(baseURL() + "/api/tasks/" + args[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()
		var task map[string]any
		json.NewDecoder(resp.Body).Decode(&task)
		if resp.StatusCode == 404 {
			fmt.Fprintf(os.Stderr, "task %s not found\n", args[0])
			os.Exit(1)
		}
		data, _ := json.MarshalIndent(task, "", "  ")
		fmt.Println(string(data))

	case "update":
		if len(args) < 2 {
			fmt.Println("Usage: waggle task update <id> --status ready")
			os.Exit(1)
		}
		taskID := args[0]
		updates := map[string]any{}
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--status":
				if i+1 < len(args) {
					updates["status"] = args[i+1]
					i++
				}
			case "--priority":
				if i+1 < len(args) {
					updates["priority"] = args[i+1]
					i++
				}
			case "--title":
				if i+1 < len(args) {
					updates["title"] = args[i+1]
					i++
				}
			}
		}
		body, _ := json.Marshal(updates)
		req, _ := http.NewRequest(http.MethodPatch, baseURL()+"/api/tasks/"+taskID, strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()
		fmt.Printf("Updated task %s\n", taskID)

	case "claim":
		if len(args) < 1 {
			fmt.Println("Usage: waggle task claim <id>")
			os.Exit(1)
		}
		body, _ := json.Marshal(map[string]string{"agent": getAgentName()})
		resp, err := http.Post(baseURL()+"/api/tasks/"+args[0]+"/claim", "application/json", strings.NewReader(string(body)))
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			var errResp map[string]any
			json.NewDecoder(resp.Body).Decode(&errResp)
			fmt.Fprintf(os.Stderr, "error: %v\n", errResp)
			os.Exit(1)
		}
		fmt.Printf("Claimed task %s\n", args[0])

	case "done":
		if len(args) < 1 {
			fmt.Println("Usage: waggle task done <id>")
			os.Exit(1)
		}
		resp, err := http.Post(baseURL()+"/api/tasks/"+args[0]+"/complete", "application/json", nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()
		fmt.Printf("Completed task %s\n", args[0])

	case "rm":
		if len(args) < 1 {
			fmt.Println("Usage: waggle task rm <id>")
			os.Exit(1)
		}
		req, _ := http.NewRequest(http.MethodDelete, baseURL()+"/api/tasks/"+args[0], nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			var errResp map[string]any
			json.NewDecoder(resp.Body).Decode(&errResp)
			fmt.Fprintf(os.Stderr, "error: %v\n", errResp)
			os.Exit(1)
		}
		fmt.Printf("Deleted task %s\n", args[0])
	}
}

func cmdAgents() {
	resp, err := http.Get(baseURL() + "/api/agents")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	var agents []map[string]any
	json.NewDecoder(resp.Body).Decode(&agents)
	if len(agents) == 0 {
		fmt.Println("No agents connected")
		return
	}
	for _, a := range agents {
		task := ""
		if t, ok := a["current_task"].(string); ok && t != "" {
			task = " working on " + t
		}
		fmt.Printf("  %s (%s) [%s]%s\n", a["name"], a["type"], a["status"], task)
	}
}

func baseURL() string {
	port := os.Getenv("WAGGLE_PORT")
	if port == "" {
		port = "4740"
	}
	return "http://localhost:" + port
}

func getAgentName() string {
	name := os.Getenv("WAGGLE_AGENT")
	if name == "" {
		hostname, _ := os.Hostname()
		name = "cli-" + hostname
	}
	return name
}

func statusIcon(status any) string {
	s, _ := status.(string)
	switch s {
	case "backlog":
		return "[ ]"
	case "ready":
		return "[>]"
	case "in_progress":
		return "[~]"
	case "review":
		return "[?]"
	case "done":
		return "[x]"
	case "blocked":
		return "[!]"
	default:
		return "[?]"
	}
}

func cmdStop() {
	// Send shutdown signal by checking if server is running, then kill it
	// For now, just check and inform — daemon mode would use a PID file
	resp, err := http.Get(baseURL() + "/health")
	if err != nil {
		fmt.Println("waggle server is not running")
		return
	}
	resp.Body.Close()
	fmt.Println("To stop waggle, press Ctrl+C in the terminal running 'waggle start'")
	fmt.Println("(Daemon mode with 'waggle stop' support coming in a future release)")
}

func cmdBackup() {
	home, _ := os.UserHomeDir()
	src := filepath.Join(home, ".waggle", "waggle.db")
	if _, err := os.Stat(src); os.IsNotExist(err) {
		fmt.Println("No database found at", src)
		os.Exit(1)
	}

	backupDir := filepath.Join(home, ".waggle", "backups")
	os.MkdirAll(backupDir, 0755)

	ts := time.Now().Format("2006-01-02-150405")
	dst := filepath.Join(backupDir, "waggle-"+ts+".db")

	data, err := os.ReadFile(src)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading database: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(dst, data, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "error writing backup: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Backed up to %s\n", dst)
}

func cmdReset() {
	fmt.Print("This will delete all waggle data. Are you sure? (y/N) ")
	var answer string
	fmt.Scanln(&answer)
	if answer != "y" && answer != "Y" {
		fmt.Println("Cancelled")
		return
	}

	home, _ := os.UserHomeDir()
	dbPath := filepath.Join(home, ".waggle", "waggle.db")
	for _, suffix := range []string{"", "-wal", "-shm"} {
		os.Remove(dbPath + suffix)
	}
	fmt.Println("Database reset complete")
}

func cmdMCP() {
	adapter := mcp.NewAdapter(baseURL())
	if err := adapter.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "mcp error: %v\n", err)
		os.Exit(1)
	}
}

func cmdConnect() {
	// Generate .mcp.json for Claude Code
	mcpConfig := map[string]any{
		"mcpServers": map[string]any{
			"waggle": map[string]any{
				"command": "waggle",
				"args":    []string{"mcp"},
			},
		},
	}

	// Check if .mcp.json exists and merge
	existing := map[string]any{}
	if data, err := os.ReadFile(".mcp.json"); err == nil {
		json.Unmarshal(data, &existing)
	}

	if servers, ok := existing["mcpServers"].(map[string]any); ok {
		servers["waggle"] = mcpConfig["mcpServers"].(map[string]any)["waggle"]
	} else {
		existing = mcpConfig
	}

	data, _ := json.MarshalIndent(existing, "", "  ")
	if err := os.WriteFile(".mcp.json", data, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "error writing .mcp.json: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Updated .mcp.json with waggle MCP server configuration")
}

func cmdWatch() {
	url := baseURL() + "/api/events"
	params := []string{}
	for i := 0; i < len(os.Args[2:]); i++ {
		switch os.Args[i+2] {
		case "--agent":
			if i+1 < len(os.Args[2:]) {
				params = append(params, "agent="+os.Args[i+3])
				i++
			}
		case "--task":
			if i+1 < len(os.Args[2:]) {
				params = append(params, "task="+os.Args[i+3])
				i++
			}
		}
	}
	if len(params) > 0 {
		url += "?" + strings.Join(params, "&")
	}

	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	fmt.Println("Watching events (Ctrl+C to stop)...")

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			data := line[6:]
			var evt map[string]any
			if json.Unmarshal([]byte(data), &evt) == nil {
				ts := ""
				if t, ok := evt["timestamp"].(string); ok {
					if parsed, err := time.Parse(time.RFC3339, t); err == nil {
						ts = parsed.Format("15:04:05")
					}
				}
				fmt.Printf("[%s] %s", ts, evt["type"])
				if aid, ok := evt["agent_id"].(string); ok && aid != "" {
					fmt.Printf(" agent=%s", aid)
				}
				if tid, ok := evt["task_id"].(string); ok && tid != "" {
					fmt.Printf(" task=%s", tid)
				}
				fmt.Println()
			}
		}
	}
}

func cmdMsg(subcmd string, args []string) {
	switch subcmd {
	case "send":
		if len(args) < 2 {
			fmt.Println("Usage: waggle msg send <agent> \"message\"")
			os.Exit(1)
		}
		body, _ := json.Marshal(map[string]string{
			"from": getAgentName(),
			"to":   args[0],
			"body": args[1],
		})
		resp, err := http.Post(baseURL()+"/api/messages", "application/json", strings.NewReader(string(body)))
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()
		fmt.Printf("Sent message to %s\n", args[0])

	case "list":
		agent := getAgentName()
		if len(args) > 0 {
			agent = args[0]
		}
		resp, err := http.Get(baseURL() + "/api/messages?to=" + agent)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()
		var msgs []map[string]any
		json.NewDecoder(resp.Body).Decode(&msgs)
		if len(msgs) == 0 {
			fmt.Println("No messages")
			return
		}
		for _, m := range msgs {
			fmt.Printf("  [%s] %s: %s\n", m["from"], m["created_at"], m["body"])
		}
	}
}

func printUsage() {
	fmt.Println(`waggle - AI agent orchestration

Usage:
  waggle start [--port 4740]       Start the server
  waggle stop                      Stop the server
  waggle status                    Server status + connected agents
  waggle mcp                       Start MCP stdio adapter
  waggle connect                   Generate .mcp.json for Claude Code

  waggle task add "title" [flags]  Create a task
    --priority high                  Set priority (critical|high|medium|low)
    --criteria "criterion"           Add acceptance criterion (repeatable)
    --tag backend                    Add tag (repeatable)
    --estimate 2h                    Time estimate
    --parent wg-xxx                  Parent task ID
    --depends wg-xxx                 Dependency (repeatable)
  waggle task list [--status X]    List tasks
  waggle task show <id>            Show task detail
  waggle task update <id> [flags]  Update a task
  waggle task claim <id>           Claim a task
  waggle task done <id>            Mark task complete
  waggle task rm <id>              Delete a task
  waggle tasks                     Shorthand for task list

  waggle agents                    List connected agents
  waggle watch [--agent X]         Tail event stream (SSE)
  waggle msg send <agent> "msg"    Send a message
  waggle msg list [agent]          List messages

  waggle backup                    Backup database
  waggle reset                     Wipe database (with confirmation)
  waggle version                   Show version`)
}
