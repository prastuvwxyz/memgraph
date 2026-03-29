package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/prastuvwxyz/memgraph/internal/config"
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init [dir]",
	Short: "Scaffold .memgraph/ in a directory",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runInit,
}

func runInit(cmd *cobra.Command, args []string) error {
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}

	abs, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}

	memgraphDir := filepath.Join(abs, ".memgraph")
	if _, err := os.Stat(memgraphDir); err == nil {
		return fmt.Errorf(".memgraph/ already exists in %s", abs)
	}

	if err := os.MkdirAll(memgraphDir, 0755); err != nil {
		return fmt.Errorf("create .memgraph/: %w", err)
	}

	configPath := filepath.Join(memgraphDir, "config.toml")
	if err := os.WriteFile(configPath, []byte(config.DefaultConfigTOML), 0644); err != nil {
		return fmt.Errorf("write config.toml: %w", err)
	}

	fmt.Printf("Initialized memgraph in %s/\n", dir)
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Printf("  memgraph index %s        # index this directory\n", dir)
	fmt.Println(`  memgraph query "text"   # search your notes`)
	fmt.Println("  memgraph --help         # see all commands")
	fmt.Println()
	fmt.Println("Docs: https://github.com/prastuvwxyz/memgraph")

	return nil
}
