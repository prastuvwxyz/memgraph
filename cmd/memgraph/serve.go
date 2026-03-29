package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	_ "embed"

	"github.com/prastuvwxyz/memgraph/internal/config"
	"github.com/prastuvwxyz/memgraph/internal/index"
	"github.com/prastuvwxyz/memgraph/internal/rank"
	"github.com/spf13/cobra"
)

//go:embed static/index.html
var indexHTML []byte

var (
	servePort int
	serveOpen bool
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start a local web server with an interactive graph UI",
	RunE:  runServe,
}

func init() {
	serveCmd.Flags().IntVar(&servePort, "port", 7331, "port to listen on")
	serveCmd.Flags().BoolVar(&serveOpen, "open", true, "open browser automatically")
}

type graphNode struct {
	ID    string   `json:"id"`
	Title string   `json:"title"`
	Tags  []string `json:"tags"`
	Group string   `json:"group"`
	Links int      `json:"links"`
}

type graphLink struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

type graphData struct {
	Nodes []graphNode `json:"nodes"`
	Links []graphLink `json:"links"`
}

func runServe(cmd *cobra.Command, args []string) error {
	dir := "."
	if dirFlag != "" {
		dir = dirFlag
	}

	abs, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}

	workspace := config.LoadOrDefault(abs)

	dbPath := filepath.Join(workspace.Root, ".memgraph", "index.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		fmt.Fprintln(os.Stderr, "No index found. Run: memgraph index .")
		os.Exit(1)
	}

	db, err := index.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open index: %w", err)
	}
	defer db.Close()

	sqlDB := db.SqlDB()

	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(indexHTML)
	})

	mux.HandleFunc("/api/graph", func(w http.ResponseWriter, r *http.Request) {
		data, buildErr := buildGraphData(sqlDB)
		if buildErr != nil {
			http.Error(w, buildErr.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(data)
	})

	mux.HandleFunc("/api/search", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		if q == "" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte("[]"))
			return
		}
		results, searchErr := rank.Search(sqlDB, q, rank.SearchOpts{TopN: 20})
		if searchErr != nil {
			http.Error(w, searchErr.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(results)
	})

	addr := fmt.Sprintf(":%d", servePort)
	url := fmt.Sprintf("http://localhost:%d", servePort)
	fmt.Printf("memgraph serve → %s\n", url)
	fmt.Println("Press Ctrl+C to stop.")

	if serveOpen {
		go openBrowser(url)
	}

	return http.ListenAndServe(addr, mux)
}

func buildGraphData(db *sql.DB) (*graphData, error) {
	rows, err := db.Query(`SELECT path, title, tags, links_out FROM notes`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	pathSet := make(map[string]bool)
	var nodes []graphNode
	var allLinks []graphLink

	for rows.Next() {
		var path, title, tagsJSON, linksJSON string
		if err := rows.Scan(&path, &title, &tagsJSON, &linksJSON); err != nil {
			return nil, err
		}

		pathSet[path] = true

		tags := parseTagsJSON(tagsJSON)
		group := pathGroup(path)

		var rawLinks []string
		if linksJSON != "" && linksJSON != "null" {
			json.Unmarshal([]byte(linksJSON), &rawLinks)
		}

		nodes = append(nodes, graphNode{
			ID:    path,
			Title: title,
			Tags:  tags,
			Group: group,
			Links: len(rawLinks),
		})

		for _, target := range rawLinks {
			allLinks = append(allLinks, graphLink{Source: path, Target: target})
		}
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Only include links where both endpoints are indexed.
	validLinks := make([]graphLink, 0, len(allLinks))
	for _, l := range allLinks {
		if pathSet[l.Target] {
			validLinks = append(validLinks, l)
		}
	}

	return &graphData{Nodes: nodes, Links: validLinks}, nil
}

func parseTagsJSON(tagsJSON string) []string {
	if tagsJSON == "" || tagsJSON == "null" {
		return nil
	}
	var tags []string
	json.Unmarshal([]byte(tagsJSON), &tags)
	return tags
}

func pathGroup(path string) string {
	idx := strings.IndexByte(path, '/')
	if idx < 0 {
		return "root"
	}
	return path[:idx]
}

func openBrowser(url string) {
	var c *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		c = exec.Command("open", url)
	case "linux":
		c = exec.Command("xdg-open", url)
	default:
		return
	}
	c.Start()
}
