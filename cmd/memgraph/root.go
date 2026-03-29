package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var dirFlag string

func Execute(version string) {
	rootCmd := &cobra.Command{
		Use:     "memgraph",
		Short:   "A local knowledge graph for your notes",
		Version: version,
	}

	rootCmd.SetVersionTemplate(fmt.Sprintf("memgraph version %s\n", version))

	rootCmd.PersistentFlags().StringVar(&dirFlag, "dir", "", "override working directory (default: current dir)")

	versionCmd := &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("memgraph version %s\n", version)
		},
	}

	rootCmd.AddCommand(initCmd, indexCmd, queryCmd, graphCmd, statsCmd, serveCmd, versionCmd)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
