package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type config struct {
	port            string
	githubToken     string
	webhookSecret   string
	vrBaseURL       string
	vrAPIKey        string
	dockerImage     string
	testCommands    [][]string // e.g. [["go","test","./..."],["go","vet","./..."]]
	maxRunSeconds   int
	slackWebhookURL string // SLACK_WEBHOOK_URL — optional
	teamsWebhookURL string // TEAMS_WEBHOOK_URL — optional
	notifyOnSuccess bool   // NOTIFY_ON_SUCCESS — default true
}

func loadConfig() (*config, error) {
	cfg := &config{
		port:          getEnv("PORT", ":8080"),
		githubToken:   os.Getenv("GITHUB_TOKEN"),
		webhookSecret: os.Getenv("GITHUB_WEBHOOK_SECRET"),
		vrBaseURL:     strings.TrimRight(os.Getenv("VAULTRUN_BASE_URL"), "/"),
		vrAPIKey:      os.Getenv("VAULTRUN_API_KEY"),
		dockerImage:   getEnv("CI_DOCKER_IMAGE", "ubuntu:22.04"),
		maxRunSeconds: 600,
	}

	if cfg.vrBaseURL == "" || cfg.vrAPIKey == "" {
		return nil, fmt.Errorf("VAULTRUN_BASE_URL and VAULTRUN_API_KEY are required")
	}
	if cfg.githubToken == "" {
		return nil, fmt.Errorf("GITHUB_TOKEN is required")
	}
	if cfg.webhookSecret == "" {
		return nil, fmt.Errorf("GITHUB_WEBHOOK_SECRET is required")
	}

	// CI_TEST_COMMANDS: JSON array of command arrays.
	// Default: run make test if a Makefile exists, else go test ./...
	raw := getEnv("CI_TEST_COMMANDS", `[["make","test"]]`)
	if err := json.Unmarshal([]byte(raw), &cfg.testCommands); err != nil {
		return nil, fmt.Errorf("CI_TEST_COMMANDS must be a JSON array of command arrays: %w", err)
	}
	if len(cfg.testCommands) == 0 {
		return nil, fmt.Errorf("CI_TEST_COMMANDS must contain at least one command")
	}
	for i, cmd := range cfg.testCommands {
		if len(cmd) == 0 {
			return nil, fmt.Errorf("CI_TEST_COMMANDS[%d] must not be empty", i)
		}
	}

	cfg.slackWebhookURL = os.Getenv("SLACK_WEBHOOK_URL")
	cfg.teamsWebhookURL = os.Getenv("TEAMS_WEBHOOK_URL")
	cfg.notifyOnSuccess = getEnv("NOTIFY_ON_SUCCESS", "true") != "false"

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
