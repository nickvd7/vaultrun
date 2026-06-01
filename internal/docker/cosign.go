package docker

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
)

// VerifyImage checks the container image's cryptographic signature using
// cosign when a public key is configured via COSIGN_PUBLIC_KEY.
//
// Behaviour:
//   - Key NOT set → no-op, returns nil (verification opt-out)
//   - Key set, cosign binary missing → returns error (fail-closed)
//   - Key set, signature absent/invalid → returns error (fail-closed)
//   - Key set, signature valid → logs and returns nil
//
// This must be called before PullImage / ImageExists so that unverified images
// are never instantiated as containers.
func (c *Client) VerifyImage(ctx context.Context, image string) error {
	if c.cosignPublicKey == "" {
		return nil // verification disabled — operator opt-out
	}

	cosignPath, err := exec.LookPath("cosign")
	if err != nil {
		// Fail closed: the operator enabled verification but the binary is absent.
		return fmt.Errorf(
			"cosign binary not found on PATH but COSIGN_PUBLIC_KEY is set — "+
				"install cosign or unset COSIGN_PUBLIC_KEY: %w", err,
		)
	}

	// Validate image name: reject argument injection (leading '-'), null bytes,
	// newlines, and other non-printable characters that could confuse cosign or
	// cause a mismatch between what is verified and what Docker actually pulls.
	if strings.HasPrefix(image, "-") {
		return fmt.Errorf("invalid image name: must not start with '-'")
	}
	if strings.ContainsAny(image, "\x00\n\r") {
		return fmt.Errorf("invalid image name: contains disallowed control characters")
	}

	cmd := exec.CommandContext(ctx, cosignPath, "verify",
		"--key", c.cosignPublicKey,
		"--",
		image,
	)

	out, err := cmd.CombinedOutput()
	if err != nil {
		slog.Error("docker: cosign verification failed",
			"image", image,
			"output", strings.TrimSpace(string(out)),
			"err", err,
		)
		return fmt.Errorf("image signature verification failed for %q: %w", image, err)
	}

	slog.Info("docker: image signature verified", "image", image)
	return nil
}
