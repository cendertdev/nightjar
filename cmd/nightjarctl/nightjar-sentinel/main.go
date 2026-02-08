// nightjar-sentinel is a CLI tool for querying Nightjar constraint data.
//
// Installation:
//
//	go build -o nightjar-sentinel ./cmd/nightjar-sentinel
//	mv nightjar-sentinel /usr/local/bin/
//
// Usage:
//
//	nightjar-sentinel query -n my-namespace
//	nightjar-sentinel explain -n my-namespace "connection refused"
//	nightjar-sentinel check -f manifest.yaml
//	nightjar-sentinel remediate -n my-namespace my-constraint
//	nightjar-sentinel status
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	version   = "dev"
	outputFmt string
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "nightjar-sentinel",
		Short: "Query and explain Nightjar constraints",
		Long: `nightjar-sentinel is a CLI tool for interacting with Nightjar.

It reads ConstraintReport CRDs and Events directly from the cluster,
providing structured JSON output that matches MCP response schemas.`,
		Version: version,
	}

	// Global flags
	rootCmd.PersistentFlags().StringVarP(&outputFmt, "output", "o", "table", "Output format: table, json, yaml")

	// Add subcommands
	rootCmd.AddCommand(queryCmd())
	rootCmd.AddCommand(explainCmd())
	rootCmd.AddCommand(checkCmd())
	rootCmd.AddCommand(remediateCmd())
	rootCmd.AddCommand(statusCmd())

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
