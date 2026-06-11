package commands

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func NewSnapshotCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "snapshot",
		Short: "Manage workspace snapshots",
	}
	cmd.AddCommand(newSnapshotCreateCmd())
	cmd.AddCommand(newSnapshotListCmd())
	cmd.AddCommand(newSnapshotDownloadCmd())
	cmd.AddCommand(newSnapshotDeleteCmd())
	return cmd
}

func newSnapshotCreateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "create <session-id> <name>",
		Short: "Create a snapshot of a session's workspace",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			snap, err := c.CreateSnapshot(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			fmt.Printf("snapshot created: id=%s name=%s size=%d bytes\n",
				snap.ID, snap.Name, snap.SizeBytes)
			return nil
		},
	}
}

func newSnapshotListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <session-id>",
		Short: "List snapshots for a session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			snaps, err := c.ListSnapshots(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if len(snaps) == 0 {
				fmt.Println("no snapshots found")
				return nil
			}
			fmt.Printf("%-36s  %-20s  %10s  %s\n", "ID", "NAME", "SIZE", "CREATED")
			for _, s := range snaps {
				fmt.Printf("%-36s  %-20s  %10d  %s\n",
					s.ID, s.Name, s.SizeBytes, s.CreatedAt.Format("2006-01-02 15:04:05"))
			}
			return nil
		},
	}
}

func newSnapshotDownloadCmd() *cobra.Command {
	var outFile string
	cmd := &cobra.Command{
		Use:   "download <snapshot-id>",
		Short: "Download a snapshot archive (.tar.gz)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			data, err := c.DownloadSnapshot(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			dest := outFile
			if dest == "" {
				dest = args[0] + ".tar.gz"
			}
			if err := os.WriteFile(dest, data, 0o644); err != nil {
				return fmt.Errorf("write file: %w", err)
			}
			fmt.Printf("snapshot saved to %s (%d bytes)\n", dest, len(data))
			return nil
		},
	}
	cmd.Flags().StringVarP(&outFile, "output", "o", "", "output file path (default: <id>.tar.gz)")
	return cmd
}

func newSnapshotDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <snapshot-id>",
		Short: "Delete a snapshot",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			if err := c.DeleteSnapshot(cmd.Context(), args[0]); err != nil {
				return err
			}
			fmt.Printf("snapshot %s deleted\n", args[0])
			return nil
		},
	}
}
