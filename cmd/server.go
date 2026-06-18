package cmd

import (
	"github.com/spf13/cobra"

	"github.com/iyear/tdl/app/web"
	"github.com/iyear/tdl/pkg/kv"
)

func NewServer() *cobra.Command {
	var opts web.Options

	cmd := &cobra.Command{
		Use:     "server",
		Aliases: []string{"web"},
		Short:   "Start a local web UI for multi-account login and uploads",
		GroupID: groupTools.ID,
		RunE: func(cmd *cobra.Command, args []string) error {
			return web.Run(cmd.Context(), kv.From(cmd.Context()), opts)
		},
	}

	cmd.Flags().StringVar(&opts.Host, "host", "127.0.0.1", "host to bind (keep it local; the API has no auth)")
	cmd.Flags().IntVarP(&opts.Port, "port", "P", 8080, "port to listen on")

	return cmd
}
