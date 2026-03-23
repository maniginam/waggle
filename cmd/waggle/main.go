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

	"github.com/maniginam/waggle/internal/mcp"
	"github.com/maniginam/waggle/internal/server"
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
			fmt.Println("Usage: waggle task <add|list|show|update|claim|done|comment|rm|next> [args]")
			os.Exit(1)
		}
		cmdTask(os.Args[2], os.Args[3:])
	case "tasks":
		cmdTask("list", os.Args[2:])
	case "agents":
		cmdAgents()
	case "agent":
		if len(os.Args) < 4 || os.Args[2] != "show" {
			fmt.Println("Usage: waggle agent show <name>")
			os.Exit(1)
		}
		cmdAgentShow(os.Args[3])
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
	case "config":
		cmdConfig(os.Args[2:])
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

func pidFilePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".waggle", "waggle.pid")
}

func writePID() {
	home, _ := os.UserHomeDir()
	os.MkdirAll(filepath.Join(home, ".waggle"), 0755)
	os.WriteFile(pidFilePath(), []byte(fmt.Sprintf("%d", os.Getpid())), 0644)
}

func removePID() {
	os.Remove(pidFilePath())
}

func readPID() (int, error) {
	data, err := os.ReadFile(pidFilePath())
	if err != nil {
		return 0, err
	}
	var pid int
	_, err = fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &pid)
	return pid, err
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

	writePID()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		removePID()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()

	if err := srv.Start(); err != nil && err != http.ErrServerClosed {
		removePID()
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
	if pid, err := readPID(); err == nil {
		fmt.Printf("waggle server is running (pid %d)\n", pid)
	} else {
		fmt.Println("waggle server is running")
	}

	// Show stats
	statsResp, err := http.Get(baseURL() + "/api/stats")
	if err == nil {
		defer statsResp.Body.Close()
		var stats map[string]any
		json.NewDecoder(statsResp.Body).Decode(&stats)

		totalTasks := int(stats["total_tasks"].(float64))
		totalAgents := int(stats["total_agents"].(float64))
		unread := int(stats["unread_messages"].(float64))

		fmt.Printf("\nTasks: %d", totalTasks)
		if totalTasks > 0 {
			byStatus := stats["tasks_by_status"].(map[string]any)
			parts := []string{}
			for _, s := range []string{"ready", "in_progress", "blocked", "review", "done", "backlog"} {
				if v, ok := byStatus[s]; ok {
					parts = append(parts, fmt.Sprintf("%s: %d", s, int(v.(float64))))
				}
			}
			if len(parts) > 0 {
				fmt.Printf(" (%s)", strings.Join(parts, ", "))
			}
		}
		fmt.Println()

		fmt.Printf("Agents: %d\n", totalAgents)
		if unread > 0 {
			fmt.Printf("Unread messages: %d\n", unread)
		}
	}

	// Show connected agents
	agentResp, err := http.Get(baseURL() + "/api/agents")
	if err == nil {
		defer agentResp.Body.Close()
		var agents []map[string]any
		json.NewDecoder(agentResp.Body).Decode(&agents)
		active := []map[string]any{}
		for _, a := range agents {
			if a["status"] != "disconnected" {
				active = append(active, a)
			}
		}
		if len(active) > 0 {
			fmt.Println()
			for _, a := range active {
				task := ""
				if t, ok := a["current_task"].(string); ok && t != "" {
					task = " working on " + t
				}
				fmt.Printf("  %s (%s) [%s]%s\n", a["name"], a["type"], a["status"], task)
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
			case "--description", "--desc":
				if i+1 < len(args) {
					task["description"] = args[i+1]
					i++
				}
			case "--status":
				if i+1 < len(args) {
					task["status"] = args[i+1]
					i++
				}
			case "--priority":
				if i+1 < len(args) {
					task["priority"] = args[i+1]
					i++
				}
			case "--deadline":
				if i+1 < len(args) {
					task["deadline"] = args[i+1]
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
			case "--tag":
				if i+1 < len(args) {
					params = append(params, "tag="+args[i+1])
					i++
				}
			case "--search", "-q":
				if i+1 < len(args) {
					params = append(params, "q="+args[i+1])
					i++
				}
			case "--sort":
				if i+1 < len(args) {
					params = append(params, "sort="+args[i+1])
					i++
				}
			case "--order":
				if i+1 < len(args) {
					params = append(params, "order="+args[i+1])
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

		// Pretty format
		fmt.Printf("%s %s [%s] %s\n", task["id"], statusIcon(task["status"]), task["priority"], task["title"])
		if desc, ok := task["description"].(string); ok && desc != "" {
			fmt.Printf("  %s\n", desc)
		}
		if assignee, ok := task["assignee"].(string); ok && assignee != "" {
			fmt.Printf("  Assigned to: %s\n", assignee)
		}
		if tags, ok := task["tags"].([]any); ok && len(tags) > 0 {
			tagStrs := make([]string, len(tags))
			for i, t := range tags {
				tagStrs[i] = fmt.Sprintf("%v", t)
			}
			fmt.Printf("  Tags: %s\n", strings.Join(tagStrs, ", "))
		}
		if criteria, ok := task["criteria"].([]any); ok && len(criteria) > 0 {
			fmt.Println("  Criteria:")
			for _, c := range criteria {
				fmt.Printf("    - %v\n", c)
			}
		}
		if deps, ok := task["depends_on"].([]any); ok && len(deps) > 0 {
			depStrs := make([]string, len(deps))
			for i, d := range deps {
				depStrs[i] = fmt.Sprintf("%v", d)
			}
			fmt.Printf("  Depends on: %s\n", strings.Join(depStrs, ", "))
		}
		if est, ok := task["estimate"].(string); ok && est != "" {
			fmt.Printf("  Estimate: %s\n", est)
		}
		if dl, ok := task["deadline"].(string); ok && dl != "" {
			fmt.Printf("  Deadline: %s\n", dl)
		}
		fmt.Printf("  Created: %s\n", task["created_at"])

		// Show dependency info
		depsResp, err := http.Get(baseURL() + "/api/tasks/" + args[0] + "/deps")
		if err == nil {
			defer depsResp.Body.Close()
			var deps map[string]any
			json.NewDecoder(depsResp.Body).Decode(&deps)
			if depList, ok := deps["depends_on"].([]any); ok && len(depList) > 0 {
				fmt.Println("\n  Depends on:")
				for _, d := range depList {
					dep := d.(map[string]any)
					fmt.Printf("    %s %s %s\n", statusIcon(dep["status"]), dep["id"], dep["title"])
				}
			}
			if blockList, ok := deps["blocking"].([]any); ok && len(blockList) > 0 {
				fmt.Println("\n  Blocking:")
				for _, b := range blockList {
					blk := b.(map[string]any)
					fmt.Printf("    %s %s %s\n", statusIcon(blk["status"]), blk["id"], blk["title"])
				}
			}
		}

		// Show subtask progress
		subResp, err := http.Get(baseURL() + "/api/tasks/" + args[0] + "/subtasks")
		if err == nil {
			defer subResp.Body.Close()
			var subResult map[string]any
			json.NewDecoder(subResp.Body).Decode(&subResult)
			if progress, ok := subResult["progress"].(map[string]any); ok {
				total := int(progress["total"].(float64))
				if total > 0 {
					done := int(progress["done"].(float64))
					fmt.Printf("\n  Subtasks: %d/%d done\n", done, total)
					if subs, ok := subResult["subtasks"].([]any); ok {
						for _, s := range subs {
							sub := s.(map[string]any)
							fmt.Printf("    %s %s %s\n", statusIcon(sub["status"]), sub["id"], sub["title"])
						}
					}
				}
			}
		}

		// Show comments
		commResp, err := http.Get(baseURL() + "/api/tasks/" + args[0] + "/comments")
		if err == nil {
			defer commResp.Body.Close()
			var comments []map[string]any
			json.NewDecoder(commResp.Body).Decode(&comments)
			if len(comments) > 0 {
				fmt.Printf("\n  Comments (%d):\n", len(comments))
				for _, c := range comments {
					fmt.Printf("    [%s] %s: %s\n", c["created_at"], c["author"], c["body"])
				}
			}
		}

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

	case "comment":
		if len(args) < 2 {
			fmt.Println("Usage: waggle task comment <id> \"message\" [--author name]")
			os.Exit(1)
		}
		taskID := args[0]
		body := args[1]
		author := "cli"
		for i := 2; i < len(args); i++ {
			if args[i] == "--author" && i+1 < len(args) {
				author = args[i+1]
				i++
			}
		}
		commentJSON, _ := json.Marshal(map[string]string{"author": author, "body": body})
		resp, err := http.Post(baseURL()+"/api/tasks/"+taskID+"/comments", "application/json", strings.NewReader(string(commentJSON)))
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
		fmt.Printf("Comment added to %s\n", taskID)

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

	case "next":
		url := baseURL() + "/api/tasks?status=ready"
		for i := 0; i < len(args); i++ {
			if args[i] == "--tag" && i+1 < len(args) {
				url += "&tag=" + args[i+1]
				i++
			}
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
			fmt.Println("No ready tasks available")
			return
		}
		priorityOrder := map[string]int{"critical": 0, "high": 1, "medium": 2, "low": 3}
		best := tasks[0]
		bestPri := 4
		if p, ok := best["priority"].(string); ok {
			if v, ok := priorityOrder[p]; ok {
				bestPri = v
			}
		}
		for _, t := range tasks[1:] {
			pri := 4
			if p, ok := t["priority"].(string); ok {
				if v, ok := priorityOrder[p]; ok {
					pri = v
				}
			}
			if pri < bestPri {
				best = t
				bestPri = pri
			}
		}
		assignee := ""
		if a, ok := best["assignee"].(string); ok && a != "" {
			assignee = " -> " + a
		}
		fmt.Printf("Next task:\n  [%s] %s %-12s %s%s\n", best["id"], statusIcon(best["status"]), best["priority"], best["title"], assignee)
		if desc, ok := best["description"].(string); ok && desc != "" {
			fmt.Printf("  %s\n", desc)
		}
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

func cmdAgentShow(name string) {
	resp, err := http.Get(baseURL() + "/api/agents/" + name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		fmt.Fprintf(os.Stderr, "agent %s not found\n", name)
		os.Exit(1)
	}
	var agent map[string]any
	json.NewDecoder(resp.Body).Decode(&agent)
	data, _ := json.MarshalIndent(agent, "", "  ")
	fmt.Println(string(data))
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

func cmdConfig(args []string) {
	home, _ := os.UserHomeDir()
	configPath := filepath.Join(home, ".waggle", "config.json")

	// Load existing config
	config := map[string]string{
		"port":              "4740",
		"event_retention":   "30d",
		"message_retention": "7d",
	}
	if data, err := os.ReadFile(configPath); err == nil {
		json.Unmarshal(data, &config)
	}

	if len(args) == 0 {
		// Show all config
		for k, v := range config {
			fmt.Printf("  %s = %s\n", k, v)
		}
		return
	}

	if len(args) == 1 {
		// Get single key
		if v, ok := config[args[0]]; ok {
			fmt.Println(v)
		} else {
			fmt.Fprintf(os.Stderr, "unknown config key: %s\n", args[0])
			os.Exit(1)
		}
		return
	}

	// Set key=value
	config[args[0]] = args[1]
	os.MkdirAll(filepath.Dir(configPath), 0755)
	data, _ := json.MarshalIndent(config, "", "  ")
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "error writing config: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Set %s = %s\n", args[0], args[1])
}

func cmdStop() {
	pid, err := readPID()
	if err != nil {
		// No PID file — check if server is running anyway
		if _, err := http.Get(baseURL() + "/health"); err != nil {
			fmt.Println("waggle server is not running")
		} else {
			fmt.Println("waggle server is running but no PID file found")
			fmt.Println("Stop it manually with Ctrl+C in the terminal running 'waggle start'")
		}
		return
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error finding process %d: %v\n", pid, err)
		removePID()
		return
	}

	if err := process.Signal(syscall.SIGTERM); err != nil {
		fmt.Fprintf(os.Stderr, "error stopping waggle (pid %d): %v\n", pid, err)
		removePID()
		return
	}

	// Wait for shutdown
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)
		if _, err := http.Get(baseURL() + "/health"); err != nil {
			fmt.Println("waggle server stopped")
			return
		}
	}
	fmt.Println("waggle server may still be shutting down")
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
	url := baseURL()
	// Auto-start server if not running
	if _, err := http.Get(url + "/health"); err != nil {
		fmt.Fprintf(os.Stderr, "waggle: auto-starting server...\n")
		go func() {
			srv, err := server.New(server.Config{})
			if err != nil {
				fmt.Fprintf(os.Stderr, "waggle: failed to start server: %v\n", err)
				return
			}
			srv.Start()
		}()
		// Wait for server to be ready
		for i := 0; i < 50; i++ {
			time.Sleep(100 * time.Millisecond)
			if _, err := http.Get(url + "/health"); err == nil {
				break
			}
		}
	}

	adapter := mcp.NewAdapter(url)
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
    --desc "description"             Task description
    --status ready                   Initial status
    --priority high                  Priority (critical|high|medium|low)
    --criteria "criterion"           Acceptance criterion (repeatable)
    --tag backend                    Tag (repeatable)
    --estimate 2h                    Time estimate
    --deadline 2026-03-25            Deadline (RFC3339 or YYYY-MM-DD)
    --parent wg-xxx                  Parent task ID
    --depends wg-xxx                 Dependency (repeatable)
  waggle task list [--status X]    List tasks (--priority, --tag, --search/-q, --sort, --order)
  waggle task next [--tag X]       Show highest-priority ready task
  waggle task show <id>            Show task detail
  waggle task update <id> [flags]  Update a task
  waggle task claim <id>           Claim a task
  waggle task done <id>            Mark task complete
  waggle task comment <id> "msg"   Add comment to task
  waggle task rm <id>              Delete a task
  waggle tasks                     Shorthand for task list

  waggle agent show <name>         Show agent detail
  waggle agents                    List connected agents
  waggle watch [--agent X]         Tail event stream (SSE)
  waggle msg send <agent> "msg"    Send a message
  waggle msg list [agent]          List messages

  waggle config                    Show all config
  waggle config <key>              Get config value
  waggle config <key> <value>      Set config value
  waggle backup                    Backup database
  waggle reset                     Wipe database (with confirmation)
  waggle version                   Show version`)
}
