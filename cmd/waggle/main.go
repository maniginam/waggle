package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/maniginam/waggle/internal/mcp"
	"github.com/maniginam/waggle/internal/server"
	_ "modernc.org/sqlite"
)

// Set at build time with: go install -ldflags "-X main.version=..." ./cmd/waggle
var version = "dev"

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
	case "project":
		if len(os.Args) < 3 {
			fmt.Println("Usage: waggle project <add|list|show|update|rm> [args]")
			os.Exit(1)
		}
		cmdProject(os.Args[2], os.Args[3:])
	case "projects":
		cmdProject("list", os.Args[2:])
	case "task":
		if len(os.Args) < 3 {
			fmt.Println("Usage: waggle task <add|list|show|update|claim|done|comment|rm|next|batch> [args]")
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
	case "review":
		if len(os.Args) < 3 {
			fmt.Println("Usage: waggle review <list|approve|reject> [args]")
			os.Exit(1)
		}
		cmdReview(os.Args[2], os.Args[3:])
	case "reviews":
		cmdReview("list", os.Args[2:])
	case "log":
		cmdLog(os.Args[2:])
	case "config":
		cmdConfig(os.Args[2:])
	case "stop":
		cmdStop()
	case "backup":
		cmdBackup()
	case "prune":
		cmdPrune()
	case "reset":
		cmdReset()
	case "tunnel":
		cmdTunnel()
	case "version":
		fmt.Printf("waggle %s\n", version)
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

	srv, err := server.New(server.Config{Port: port, Version: version})
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

	// Show version
	if vResp, err := http.Get(baseURL() + "/api/version"); err == nil {
		var ver map[string]string
		json.NewDecoder(vResp.Body).Decode(&ver)
		vResp.Body.Close()
		if pid, err := readPID(); err == nil {
			fmt.Printf("waggle %s (pid %d)\n", ver["version"], pid)
		} else {
			fmt.Printf("waggle %s\n", ver["version"])
		}
	} else if pid, err := readPID(); err == nil {
		fmt.Printf("waggle server running (pid %d)\n", pid)
	} else {
		fmt.Println("waggle server running")
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

		// Show velocity
		if velocity, ok := stats["velocity"].([]any); ok && len(velocity) > 0 {
			total := 0
			for _, v := range velocity {
				if day, ok := v.(map[string]any); ok {
					total += int(day["count"].(float64))
				}
			}
			avg := float64(total) / float64(len(velocity))
			fmt.Printf("Velocity: %d completed this week (%.1f/day)\n", total, avg)
		}
	}

	// Show pending reviews
	if revResp, err := http.Get(baseURL() + "/api/reviews?status=pending"); err == nil {
		var reviews []any
		json.NewDecoder(revResp.Body).Decode(&reviews)
		revResp.Body.Close()
		if len(reviews) > 0 {
			fmt.Printf("Pending reviews: %d\n", len(reviews))
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
			case "--type":
				if i+1 < len(args) {
					task["task_type"] = args[i+1]
					i++
				}
			case "--project":
				if i+1 < len(args) {
					task["project_id"] = args[i+1]
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
			case "--type":
				if i+1 < len(args) {
					params = append(params, "task_type="+args[i+1])
					i++
				}
			case "--project":
				if i+1 < len(args) {
					params = append(params, "project_id="+args[i+1])
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

	case "move":
		if len(args) < 2 {
			fmt.Println("Usage: waggle task move <id> <status>")
			fmt.Println("  Shorthand for: waggle task update <id> --status <status>")
			fmt.Println("  Statuses: ready, in_progress, blocked, done, review")
			os.Exit(1)
		}
		body, _ := json.Marshal(map[string]any{"status": args[1]})
		req, _ := http.NewRequest(http.MethodPatch, baseURL()+"/api/tasks/"+args[0], strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
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
		fmt.Printf("Moved task %s → %s\n", args[0], args[1])

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

	case "batch":
		if len(args) == 0 {
			fmt.Println("Usage: waggle task batch <file.json>")
			fmt.Println("  JSON file should contain an array of task objects")
			os.Exit(1)
		}
		data, err := os.ReadFile(args[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "error reading %s: %v\n", args[0], err)
			os.Exit(1)
		}
		var tasks []map[string]any
		if err := json.Unmarshal(data, &tasks); err != nil {
			fmt.Fprintf(os.Stderr, "error parsing JSON: %v\n", err)
			os.Exit(1)
		}
		created, failed := 0, 0
		for _, task := range tasks {
			body, _ := json.Marshal(task)
			resp, err := http.Post(baseURL()+"/api/tasks", "application/json", strings.NewReader(string(body)))
			if err != nil {
				fmt.Fprintf(os.Stderr, "  error creating task: %v\n", err)
				failed++
				continue
			}
			var result map[string]any
			json.NewDecoder(resp.Body).Decode(&result)
			resp.Body.Close()
			if resp.StatusCode >= 400 {
				fmt.Fprintf(os.Stderr, "  failed: %v - %v\n", task["title"], result)
				failed++
			} else {
				fmt.Printf("  Created %s: %s\n", result["id"], result["title"])
				created++
			}
		}
		fmt.Printf("\nBatch complete: %d created, %d failed\n", created, failed)
	}
}

func cmdProject(subcmd string, args []string) {
	switch subcmd {
	case "add":
		if len(args) == 0 {
			fmt.Println("Usage: waggle project add \"name\" [--desc \"description\"]")
			os.Exit(1)
		}
		project := map[string]any{"name": args[0]}
		for i := 1; i < len(args); i++ {
			if (args[i] == "--desc" || args[i] == "--description") && i+1 < len(args) {
				project["description"] = args[i+1]
				i++
			}
		}
		body, _ := json.Marshal(project)
		resp, err := http.Post(baseURL()+"/api/projects", "application/json", strings.NewReader(string(body)))
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
		fmt.Printf("Created project %s: %s\n", result["id"], result["name"])

	case "list":
		resp, err := http.Get(baseURL() + "/api/projects")
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()
		var projects []map[string]any
		json.NewDecoder(resp.Body).Decode(&projects)
		if len(projects) == 0 {
			fmt.Println("No projects")
			return
		}
		for _, p := range projects {
			desc := ""
			if d, ok := p["description"].(string); ok && d != "" {
				desc = " - " + d
			}
			fmt.Printf("  [%s] %s%s\n", p["id"], p["name"], desc)
		}

	case "show":
		if len(args) == 0 {
			fmt.Println("Usage: waggle project show <id>")
			os.Exit(1)
		}
		resp, err := http.Get(baseURL() + "/api/projects/" + args[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()
		if resp.StatusCode == 404 {
			fmt.Fprintf(os.Stderr, "project %s not found\n", args[0])
			os.Exit(1)
		}
		var project map[string]any
		json.NewDecoder(resp.Body).Decode(&project)
		fmt.Printf("%s: %s\n", project["id"], project["name"])
		if desc, ok := project["description"].(string); ok && desc != "" {
			fmt.Printf("  %s\n", desc)
		}

		// Show epics
		epicsResp, err := http.Get(baseURL() + "/api/projects/" + args[0] + "/epics")
		if err == nil {
			defer epicsResp.Body.Close()
			var epics []map[string]any
			json.NewDecoder(epicsResp.Body).Decode(&epics)
			if len(epics) > 0 {
				fmt.Printf("\n  Epics (%d):\n", len(epics))
				for _, e := range epics {
					progress := ""
					if p, ok := e["progress"].(map[string]any); ok {
						done := int(p["done"].(float64))
						total := int(p["total"].(float64))
						if total > 0 {
							progress = fmt.Sprintf(" [%d/%d]", done, total)
						}
					}
					fmt.Printf("    %s %s %s%s\n", statusIcon(e["status"]), e["id"], e["title"], progress)
				}
			}
		}

		// Show all tasks in project
		tasksResp, err := http.Get(baseURL() + "/api/tasks?project_id=" + args[0])
		if err == nil {
			defer tasksResp.Body.Close()
			var tasks []map[string]any
			json.NewDecoder(tasksResp.Body).Decode(&tasks)
			if len(tasks) > 0 {
				fmt.Printf("\n  All tasks (%d):\n", len(tasks))
				for _, t := range tasks {
					taskType := ""
					if tt, ok := t["task_type"].(string); ok && tt != "task" {
						taskType = " [" + tt + "]"
					}
					fmt.Printf("    %s %s %-12s %s%s\n", statusIcon(t["status"]), t["id"], t["priority"], t["title"], taskType)
				}
			}
		}

	case "update":
		if len(args) < 2 {
			fmt.Println("Usage: waggle project update <id> --name \"new name\" [--desc \"new desc\"]")
			os.Exit(1)
		}
		projectID := args[0]
		updates := map[string]any{}
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--name":
				if i+1 < len(args) {
					updates["name"] = args[i+1]
					i++
				}
			case "--desc", "--description":
				if i+1 < len(args) {
					updates["description"] = args[i+1]
					i++
				}
			}
		}
		body, _ := json.Marshal(updates)
		req, _ := http.NewRequest(http.MethodPatch, baseURL()+"/api/projects/"+projectID, strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()
		fmt.Printf("Updated project %s\n", projectID)

	case "rm":
		if len(args) < 1 {
			fmt.Println("Usage: waggle project rm <id>")
			os.Exit(1)
		}
		req, _ := http.NewRequest(http.MethodDelete, baseURL()+"/api/projects/"+args[0], nil)
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
		fmt.Printf("Deleted project %s\n", args[0])

	default:
		fmt.Println("Usage: waggle project <add|list|show|update|rm> [args]")
		os.Exit(1)
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

func cmdLog(args []string) {
	limit := 20
	for i := 0; i < len(args); i++ {
		if args[i] == "--limit" && i+1 < len(args) {
			fmt.Sscanf(args[i+1], "%d", &limit)
			i++
		}
	}
	url := fmt.Sprintf("%s/api/events?limit=%d", baseURL(), limit)
	resp, err := http.Get(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	var events []map[string]any
	json.NewDecoder(resp.Body).Decode(&events)
	if len(events) == 0 {
		fmt.Println("No recent events")
		return
	}
	for _, e := range events {
		ts := ""
		if t, ok := e["timestamp"].(string); ok {
			if parsed, err := time.Parse(time.RFC3339, t); err == nil {
				ts = parsed.Local().Format("15:04:05")
			}
		}
		agent := ""
		if a, ok := e["agent_id"].(string); ok && a != "" {
			agent = " " + a
		}
		task := ""
		if t, ok := e["task_id"].(string); ok && t != "" {
			task = " " + t
		}
		fmt.Printf("  [%s] %-20s%s%s\n", ts, e["type"], agent, task)
	}
}

func cmdReview(subcmd string, args []string) {
	switch subcmd {
	case "list":
		status := "pending"
		for i := 0; i < len(args); i++ {
			if args[i] == "--status" && i+1 < len(args) {
				status = args[i+1]
				i++
			}
			if args[i] == "--all" {
				status = ""
			}
		}
		url := baseURL() + "/api/reviews"
		if status != "" {
			url += "?status=" + status
		}
		resp, err := http.Get(url)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()
		var reviews []map[string]any
		json.NewDecoder(resp.Body).Decode(&reviews)
		if len(reviews) == 0 {
			fmt.Println("No reviews found")
			return
		}
		for _, r := range reviews {
			statusStr := fmt.Sprintf("%-8s", r["status"])
			agent := ""
			if a, ok := r["agent_id"].(string); ok && a != "" {
				agent = " by " + a
			}
			branch := ""
			if b, ok := r["branch"].(string); ok && b != "" {
				branch = " [" + b + "]"
			}
			fmt.Printf("  %s %s %s%s%s\n", r["id"], statusStr, r["task_id"], agent, branch)
		}

	case "approve":
		if len(args) == 0 {
			fmt.Println("Usage: waggle review approve <review-id>")
			os.Exit(1)
		}
		body, _ := json.Marshal(map[string]string{"status": "approved"})
		req, _ := http.NewRequest(http.MethodPatch, baseURL()+"/api/reviews/"+args[0], strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
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
		fmt.Printf("Approved review %s\n", args[0])

	case "reject":
		if len(args) == 0 {
			fmt.Println("Usage: waggle review reject <review-id> [\"feedback\"]")
			os.Exit(1)
		}
		feedback := ""
		if len(args) > 1 {
			feedback = args[1]
		}
		body, _ := json.Marshal(map[string]string{"status": "rejected", "feedback": feedback})
		req, _ := http.NewRequest(http.MethodPatch, baseURL()+"/api/reviews/"+args[0], strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
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
		fmt.Printf("Rejected review %s\n", args[0])

	default:
		fmt.Println("Usage: waggle review <list|approve|reject> [args]")
		os.Exit(1)
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

	// Use VACUUM INTO for a consistent snapshot (safe with WAL mode)
	db, err := sql.Open("sqlite", src+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error opening database: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	if _, err := db.Exec("VACUUM INTO ?", dst); err != nil {
		fmt.Fprintf(os.Stderr, "error backing up database: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Backed up to %s\n", dst)
}

func cmdPrune() {
	home, _ := os.UserHomeDir()
	dbPath := filepath.Join(home, ".waggle", "waggle.db")
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error opening database: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	// Cleanup stale tasks (14+ days without updates, unassigned)
	cutoff14d := time.Now().UTC().Add(-14 * 24 * time.Hour).Format(time.RFC3339)
	now := time.Now().UTC().Format(time.RFC3339)
	result, _ := db.Exec(
		"UPDATE tasks SET status = 'done', updated_at = ? WHERE status IN ('backlog', 'ready') AND assignee = '' AND updated_at < ?",
		now, cutoff14d)
	staleTasks, _ := result.RowsAffected()
	if staleTasks > 0 {
		fmt.Printf("Closed %d stale tasks (no updates for 14+ days)\n", staleTasks)
	}

	// Purge disconnected agents older than 24h
	cutoff24h := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
	result, _ = db.Exec("DELETE FROM agents WHERE status = 'disconnected' AND last_seen < ?", cutoff24h)
	purgedAgents, _ := result.RowsAffected()
	if purgedAgents > 0 {
		fmt.Printf("Purged %d disconnected agents (24+ hours old)\n", purgedAgents)
	}

	// Cleanup old events (30+ days)
	cutoff30d := time.Now().UTC().Add(-30 * 24 * time.Hour).Format(time.RFC3339)
	result, _ = db.Exec("DELETE FROM events WHERE timestamp < ?", cutoff30d)
	oldEvents, _ := result.RowsAffected()
	if oldEvents > 0 {
		fmt.Printf("Cleaned up %d old events (30+ days)\n", oldEvents)
	}

	// Cleanup old read messages (7+ days)
	cutoff7d := time.Now().UTC().Add(-7 * 24 * time.Hour).Format(time.RFC3339)
	result, _ = db.Exec("DELETE FROM messages WHERE read = 1 AND created_at < ?", cutoff7d)
	oldMsgs, _ := result.RowsAffected()
	if oldMsgs > 0 {
		fmt.Printf("Cleaned up %d old read messages (7+ days)\n", oldMsgs)
	}

	if staleTasks+purgedAgents+oldEvents+oldMsgs == 0 {
		fmt.Println("Nothing to prune")
	}
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
			srv, err := server.New(server.Config{Version: version})
			if err != nil {
				fmt.Fprintf(os.Stderr, "waggle: failed to start server: %v\n", err)
				return
			}
			srv.Start()
		}()
		// Wait for server to be ready with clearer feedback
		ready := false
		for i := 0; i < 50; i++ {
			time.Sleep(100 * time.Millisecond)
			if _, err := http.Get(url + "/health"); err == nil {
				ready = true
				break
			}
		}
		if !ready {
			fmt.Fprintf(os.Stderr, "waggle: server failed to start within 5s — check logs at /tmp/waggle.log\n")
		}
	}

	adapter := mcp.NewAdapter(url)
	if err := adapter.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "mcp error: %v\n", err)
		os.Exit(1)
	}
}

func cmdConnect() {
	// Use full path to waggle binary so it works even if Go bin isn't in PATH
	waggleBin := "waggle"
	if exe, err := os.Executable(); err == nil {
		waggleBin = exe
	}

	// Generate .mcp.json for Claude Code
	mcpConfig := map[string]any{
		"mcpServers": map[string]any{
			"waggle": map[string]any{
				"command": waggleBin,
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

	case "search":
		if len(args) == 0 {
			fmt.Println("Usage: waggle msg search \"query\"")
			os.Exit(1)
		}
		resp, err := http.Get(baseURL() + "/api/messages?q=" + args[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()
		var msgs []map[string]any
		json.NewDecoder(resp.Body).Decode(&msgs)
		if len(msgs) == 0 {
			fmt.Println("No messages matching query")
			return
		}
		for _, m := range msgs {
			fmt.Printf("  [%s → %s] %s: %s\n", m["from"], m["to"], m["created_at"], m["body"])
		}
	}
}

func cmdTunnel() {
	port := 4740
	for i, arg := range os.Args[2:] {
		if arg == "--port" && i+1 < len(os.Args[2:]) {
			fmt.Sscanf(os.Args[i+3], "%d", &port)
		}
	}

	// Check server is running first
	_, err := http.Get(fmt.Sprintf("http://localhost:%d/health", port))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: waggle server is not running on port %d\n", port)
		fmt.Fprintf(os.Stderr, "Start it first with: waggle start\n")
		os.Exit(1)
	}

	// Detect which tunnel tool is available
	type tunnelDef struct {
		name string
		bin  string
		args []string
		hint string
	}
	tunnels := []tunnelDef{
		{
			name: "Cloudflare Tunnel",
			bin:  "cloudflared",
			args: []string{"tunnel", "--url", fmt.Sprintf("http://localhost:%d", port)},
			hint: "Install: brew install cloudflared",
		},
		{
			name: "ngrok",
			bin:  "ngrok",
			args: []string{"http", fmt.Sprintf("%d", port)},
			hint: "Install: brew install ngrok",
		},
	}

	var chosen *tunnelDef
	for i := range tunnels {
		if _, err := exec.LookPath(tunnels[i].bin); err == nil {
			chosen = &tunnels[i]
			break
		}
	}

	if chosen == nil {
		fmt.Println("No tunnel tool found. Install one of:")
		for _, t := range tunnels {
			fmt.Printf("  %-20s  %s\n", t.name, t.hint)
		}
		fmt.Println("\nThen run: waggle tunnel")
		os.Exit(1)
	}

	fmt.Printf("Starting %s tunnel for waggle on port %d...\n", chosen.name, port)
	fmt.Println("Dashboard will be accessible via the public URL shown below.")
	fmt.Println("Press Ctrl+C to stop.")
	fmt.Println()

	cmd := exec.Command(chosen.bin, chosen.args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		if cmd.Process != nil {
			cmd.Process.Signal(syscall.SIGTERM)
		}
	}()

	if err := cmd.Run(); err != nil {
		if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
			fmt.Fprintf(os.Stderr, "tunnel error: %v\n", err)
			os.Exit(1)
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
  waggle tunnel [--port 4740]      Expose dashboard publicly (cloudflared or ngrok)

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
    --type epic                      Task type (task|epic|story|issue)
    --project wg-xxx                 Project ID
  waggle task list [--status X]    List tasks (--priority, --tag, --type, --project, --search/-q, --sort, --order)
  waggle task next [--tag X]       Show highest-priority ready task
  waggle task show <id>            Show task detail
  waggle task update <id> [flags]  Update a task
  waggle task claim <id>           Claim a task
  waggle task done <id>            Mark task complete
  waggle task move <id> <status>   Change task status (shorthand)
  waggle task comment <id> "msg"   Add comment to task
  waggle task rm <id>              Delete a task
  waggle task batch <file.json>   Create multiple tasks from JSON file
  waggle tasks                     Shorthand for task list

  waggle project add "name"        Create a project (--desc "description")
  waggle project list              List projects (also: waggle projects)
  waggle project show <id>         Show project with epics and tasks
  waggle project update <id>       Update project (--name, --desc)
  waggle project rm <id>           Delete a project

  waggle review list [--all]       List reviews (pending by default, --all for all)
  waggle review approve <id>      Approve a review
  waggle review reject <id> "fb" Reject with feedback
  waggle reviews                   Shorthand for review list

  waggle log [--limit 20]          Show recent activity log
  waggle agent show <name>         Show agent detail
  waggle agents                    List connected agents
  waggle watch [--agent X]         Tail event stream (SSE)
  waggle msg send <agent> "msg"    Send a message
  waggle msg list [agent]          List messages
  waggle msg search "query"        Search message history

  waggle config                    Show all config
  waggle config <key>              Get config value
  waggle config <key> <value>      Set config value
  waggle backup                    Backup database (safe with WAL)
  waggle prune                     Cleanup stale tasks, old agents/events/messages
  waggle reset                     Wipe database (with confirmation)
  waggle version                   Show version`)
}
