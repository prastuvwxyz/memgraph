package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/prastuvwxyz/memgraph/internal/cluster"
	"github.com/prastuvwxyz/memgraph/internal/config"
	"github.com/prastuvwxyz/memgraph/internal/index"
	"github.com/spf13/cobra"
)

var (
	clusterTopics int
	clusterNs     []string
	clusterPrefix string
)

var clusterCmd = &cobra.Command{
	Use:   "cluster [dir]",
	Short: "Group files by topic automatically",
	Long: `Cluster indexed files into N topic groups without needing to know keywords.
Uses vector k-means when embeddings are available; falls back to tag-graph clustering.

Example:
  memgraph cluster memory/2026-03/ --topics 5
  memgraph cluster --topics 8 --ns stella`,
	Args: cobra.MaximumNArgs(1),
	RunE: runCluster,
}

func init() {
	clusterCmd.Flags().IntVar(&clusterTopics, "topics", 5, "number of topic clusters to produce")
	clusterCmd.Flags().StringArrayVar(&clusterNs, "ns", nil, "filter by namespace(s)")
	clusterCmd.Flags().StringVar(&clusterPrefix, "prefix", "", "restrict to files under this path prefix")
}

func runCluster(cmd *cobra.Command, args []string) error {
	dir := "."
	if dirFlag != "" {
		dir = dirFlag
	}
	if len(args) > 0 {
		clusterPrefix = filepath.ToSlash(args[0])
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

	emb := resolveEmbedder(workspace.Config.Embed)

	if emb != nil {
		clusters, err := cluster.Vector(db.SqlDB(), clusterPrefix, clusterNs, clusterTopics)
		if err == nil && len(clusters) > 0 {
			printClusters(clusters, "vector")
			return nil
		}
	}

	clusters, err := cluster.Content(db.SqlDB(), clusterPrefix, clusterNs, clusterTopics)
	if err == nil && len(clusters) > 0 {
		printClusters(clusters, "content")
		return nil
	}

	clusters, err = cluster.Tag(db.SqlDB(), clusterPrefix, clusterNs, clusterTopics)
	if err != nil {
		return fmt.Errorf("cluster: %w", err)
	}
	printClusters(clusters, "tag")
	return nil
}

func printClusters(clusters []cluster.Cluster, mode string) {
	fmt.Printf("Clusters (%s-based, %d groups)\n\n", mode, len(clusters))
	for i, c := range clusters {
		fmt.Printf("── %d. [%s] (%d files)\n", i+1, c.Label, len(c.Files))
		for j, f := range c.Files {
			if j >= 8 {
				fmt.Printf("   … and %d more\n", len(c.Files)-j)
				break
			}
			fmt.Printf("   %s\n", f)
		}
		fmt.Println()
	}
}
