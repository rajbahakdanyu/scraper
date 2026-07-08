// cmd/root.go
package cmd

import "github.com/spf13/cobra"

func Execute() {
	if err := NewRootCmd().Execute(); err != nil {
		panic(err)
	}
}

func NewRootCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "scraper",
		Short: "Media scraper CLI tool",
		Long:  `Download media from various sites with a beautiful terminal UI.`,
	}
}