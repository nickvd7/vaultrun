package commands

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func NewArtifactCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "artifact",
		Short: "Manage shared artifacts",
	}
	cmd.AddCommand(newArtifactPromoteCmd())
	cmd.AddCommand(newArtifactListCmd())
	cmd.AddCommand(newArtifactDownloadCmd())
	cmd.AddCommand(newArtifactDeleteCmd())
	return cmd
}

func newArtifactPromoteCmd() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "promote <session-id> <path>",
		Short: "Promote a workspace file to the shared artifact registry",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			art, err := c.PromoteArtifact(cmd.Context(), args[0], args[1], name)
			if err != nil {
				return err
			}
			fmt.Printf("artifact created: id=%s name=%s size=%d bytes\n",
				art.ID, art.Name, art.SizeBytes)
			return nil
		},
	}
	cmd.Flags().StringVarP(&name, "name", "n", "", "artifact name (default: basename of path)")
	return cmd
}

func newArtifactListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List shared artifacts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			arts, err := c.ListArtifacts(cmd.Context())
			if err != nil {
				return err
			}
			if len(arts) == 0 {
				fmt.Println("no artifacts found")
				return nil
			}
			fmt.Printf("%-36s  %-20s  %10s  %s\n", "ID", "NAME", "SIZE", "CREATED")
			for _, a := range arts {
				fmt.Printf("%-36s  %-20s  %10d  %s\n",
					a.ID, a.Name, a.SizeBytes, a.CreatedAt.Format("2006-01-02 15:04:05"))
			}
			return nil
		},
	}
}

func newArtifactDownloadCmd() *cobra.Command {
	var outFile string
	cmd := &cobra.Command{
		Use:   "download <artifact-id>",
		Short: "Download a shared artifact",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			data, err := c.DownloadArtifact(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			dest := outFile
			if dest == "" {
				dest = args[0]
			}
			if err := os.WriteFile(dest, data, 0o644); err != nil {
				return fmt.Errorf("write file: %w", err)
			}
			fmt.Printf("artifact saved to %s (%d bytes)\n", dest, len(data))
			return nil
		},
	}
	cmd.Flags().StringVarP(&outFile, "output", "o", "", "output file path (default: artifact ID)")
	return cmd
}

func newArtifactDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <artifact-id>",
		Short: "Delete a shared artifact",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			if err := c.DeleteArtifact(cmd.Context(), args[0]); err != nil {
				return err
			}
			fmt.Printf("artifact %s deleted\n", args[0])
			return nil
		},
	}
}
