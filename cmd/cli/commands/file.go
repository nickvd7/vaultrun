package commands

import (
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func newFileCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "file",
		Short: "Manage files in a session workspace",
	}
	cmd.AddCommand(fileUploadCmd(), fileDownloadCmd(), fileListCmd(), fileDeleteCmd())
	return cmd
}

func fileUploadCmd() *cobra.Command {
	var remotePath string

	cmd := &cobra.Command{
		Use:   "upload <session-id> <local-path>",
		Short: "Upload a file to a session workspace",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			sessionID := args[0]
			localPath := args[1]

			f, err := os.Open(localPath)
			if err != nil {
				return fmt.Errorf("open file: %w", err)
			}
			defer f.Close()

			pr, pw := io.Pipe()
			mw := multipart.NewWriter(pw)

			go func() {
				defer pw.Close()
				defer mw.Close()

				// Add path field
				dest := remotePath
				if dest == "" {
					dest = filepath.Base(localPath)
				}
				_ = mw.WriteField("path", dest)

				fw, err := mw.CreateFormFile("file", filepath.Base(localPath))
				if err != nil {
					return
				}
				_, _ = io.Copy(fw, f)
			}()

			client := newClient()
			req, err := http.NewRequest("POST", client.baseURL+"/api/v1/sessions/"+sessionID+"/files", pr)
			if err != nil {
				return err
			}
			req.Header.Set("X-API-Key", client.key)
			req.Header.Set("Content-Type", mw.FormDataContentType())

			resp, err := client.http.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()

			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode >= 400 {
				return fmt.Errorf("upload failed (%d): %s", resp.StatusCode, string(body))
			}

			fmt.Printf("Uploaded %s\n", localPath)
			return nil
		},
	}

	cmd.Flags().StringVar(&remotePath, "path", "", "Remote path in workspace (default: filename)")
	return cmd
}

func fileDownloadCmd() *cobra.Command {
	var outputPath string

	cmd := &cobra.Command{
		Use:   "download <session-id> <remote-path>",
		Short: "Download a file from a session workspace",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			sessionID := args[0]
			remotePath := args[1]

			client := newClient()
			req, err := http.NewRequest("GET", client.baseURL+"/api/v1/sessions/"+sessionID+"/files/"+remotePath, nil)
			if err != nil {
				return err
			}
			req.Header.Set("X-API-Key", client.key)

			resp, err := client.http.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()

			if resp.StatusCode >= 400 {
				body, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("download failed (%d): %s", resp.StatusCode, string(body))
			}

			dest := outputPath
			if dest == "" {
				dest = filepath.Base(remotePath)
			}

			out, err := os.Create(dest)
			if err != nil {
				return fmt.Errorf("create output file: %w", err)
			}
			defer out.Close()

			_, err = io.Copy(out, resp.Body)
			if err != nil {
				return err
			}

			fmt.Printf("Downloaded to %s\n", dest)
			return nil
		},
	}

	cmd.Flags().StringVar(&outputPath, "output", "", "Local output path (default: basename of remote path)")
	return cmd
}

func fileListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <session-id>",
		Short: "List files in a session workspace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var result struct {
				Files []map[string]interface{} `json:"files"`
			}
			if err := newClient().get("/api/v1/sessions/"+args[0]+"/files", &result); err != nil {
				return err
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "PATH\tSIZE\tCONTENT-TYPE\tUPDATED")
			for _, f := range result.Files {
				fmt.Fprintf(w, "%v\t%v\t%v\t%v\n", f["path"], f["size_bytes"], f["content_type"], f["updated_at"])
			}
			return w.Flush()
		},
	}
}

func fileDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <session-id> <remote-path>",
		Short: "Delete a file from a session workspace",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[1]
			if len(path) > 0 && path[0] != '/' {
				path = "/" + path
			}
			if err := newClient().delete("/api/v1/sessions/" + args[0] + "/files" + path); err != nil {
				return err
			}
			fmt.Printf("Deleted %s\n", args[1])
			return nil
		},
	}
}
