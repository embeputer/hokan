package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	serverFlag string
	tokenFlag  string
)

type clientKey struct{}

func Execute() {
	root := &cobra.Command{
		Use:   "hokan",
		Short: "Hokan CLI — talks only to the Hokan HTTP API",
	}
	root.PersistentFlags().StringVar(&serverFlag, "server", "", "Hokan server URL (or HOKAN_SERVER)")
	root.PersistentFlags().StringVar(&tokenFlag, "token", "", "API token (or HOKAN_TOKEN)")
	root.AddCommand(authCmd(), repoCmd(), prCmd(), ciCmd())
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func clientFrom(_ *cobra.Command) *Client {
	c := New()
	if serverFlag != "" {
		c.BaseURL = serverFlag
	}
	if tokenFlag != "" {
		c.Token = tokenFlag
	}
	return c
}

func withClient(ctx context.Context, c *Client) context.Context {
	return context.WithValue(ctx, clientKey{}, c)
}
