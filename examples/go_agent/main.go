// Example: Go agent using the VaultRun Go SDK.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	vaultrun "github.com/nickvd7/vaultrun/sdk/go"
)

func main() {
	apiURL := env("VAULTRUN_API_URL", "http://localhost:8080")
	apiKey := env("VAULTRUN_API_KEY", "")

	client := vaultrun.New(apiURL, apiKey)
	ctx := context.Background()

	// 1. Create session
	fmt.Println("1. Creating session…")
	session, err := client.CreateSession(ctx, vaultrun.CreateSessionOptions{
		Name:          "go-demo",
		Image:         "python:3.12-slim",
		CPULimit:      0.5,
		MemoryLimitMB: 256,
	})
	if err != nil {
		log.Fatalf("create session: %v", err)
	}
	fmt.Printf("   Session ID: %s  Status: %s\n", session.ID, session.Status)
	defer func() {
		fmt.Println("\n5. Cleaning up session…")
		client.DeleteSession(ctx, session.ID)
	}()

	// 2. Upload a script
	script := `import sys
print("Hello from VaultRun Go example!")
print(f"Python version: {sys.version}")
`
	fmt.Println("\n2. Uploading script…")
	f, err := client.UploadFile(ctx, session.ID, "hello.py", strings.NewReader(script))
	if err != nil {
		log.Fatalf("upload: %v", err)
	}
	fmt.Printf("   Uploaded %s (%d bytes)\n", f.Path, f.SizeBytes)

	// 3. Execute
	fmt.Println("\n3. Executing script…")
	run, err := client.Run(ctx, session.ID, vaultrun.RunOptions{
		Command:        "python",
		Args:           []string{"hello.py"},
		TimeoutSeconds: 10,
	})
	if err != nil {
		log.Fatalf("run: %v", err)
	}
	fmt.Printf("   Run ID:    %s\n", run.ID)
	fmt.Printf("   Status:    %s\n", run.Status)
	fmt.Printf("   Exit code: %v\n", *run.ExitCode)
	if run.DurationMS != nil {
		fmt.Printf("   Duration:  %dms\n", *run.DurationMS)
	}

	if run.Stdout != nil && *run.Stdout != "" {
		fmt.Println("\n--- stdout ---")
		fmt.Print(*run.Stdout)
	}

	// 4. List files
	fmt.Println("\n4. Files in workspace:")
	files, err := client.ListFiles(ctx, session.ID)
	if err != nil {
		log.Printf("list files: %v", err)
	}
	for _, file := range files {
		fmt.Printf("   %s (%d bytes)\n", file.Path, file.SizeBytes)
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
