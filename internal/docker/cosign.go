package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
)

// VerifyImage checks the container image's cryptographic signature using
// cosign when a public key is configured via COSIGN_PUBLIC_KEY.
//
// Behaviour:
//   - Key NOT set → no-op, returns (image, nil)
//   - Key set, cosign binary missing → returns error (fail-closed)
//   - Key set, signature absent/invalid → returns error (fail-closed)
//   - Key set, signature valid → returns digest-pinned image ref, e.g.
//     "python@sha256:abc123…" — use this for all subsequent pull/run operations
//     to close the TOCTOU window between verify and pull.
//
// This must be called before PullImage / ImageExists so that unverified images
// are never instantiated as containers.
func (c *Client) VerifyImage(ctx context.Context, image string) (string, error) {
	if c.cosignPublicKey == "" {
		return image, nil // verification disabled — operator opt-out
	}

	cosignPath, err := exec.LookPath("cosign")
	if err != nil {
		// Fail closed: the operator enabled verification but the binary is absent.
		return "", fmt.Errorf(
			"cosign binary not found on PATH but COSIGN_PUBLIC_KEY is set — "+
				"install cosign or unset COSIGN_PUBLIC_KEY: %w", err,
		)
	}

	// Validate image name: reject argument injection (leading '-'), null bytes,
	// newlines, and other non-printable characters that could confuse cosign or
	// cause a mismatch between what is verified and what Docker actually pulls.
	if strings.HasPrefix(image, "-") {
		return "", fmt.Errorf("invalid image name: must not start with '-'")
	}
	if strings.ContainsAny(image, "\x00\n\r") {
		return "", fmt.Errorf("invalid image name: contains disallowed control characters")
	}

	// Build the cosign verify argument list. --tlog-verify requires the image
	// signature to be present in the Rekor transparency log, preventing use of
	// offline-only signatures. Opt-in via COSIGN_REQUIRE_TLOG=true because
	// private/air-gapped registries typically do not publish to Rekor.
	cosignArgs := []string{"verify", "--key", c.cosignPublicKey}
	if c.requireTlog {
		cosignArgs = append(cosignArgs, "--tlog-verify")
	}
	cosignArgs = append(cosignArgs, "--", image)

	cmd := exec.CommandContext(ctx, cosignPath, cosignArgs...)

	out, err := cmd.CombinedOutput()
	if err != nil {
		slog.Error("docker: cosign verification failed",
			"image", image,
			"output", strings.TrimSpace(string(out)),
			"err", err,
		)
		return "", fmt.Errorf("image signature verification failed for %q: %w", image, err)
	}

	// Extract the manifest digest from cosign's JSON output so we can pin the
	// image to an immutable digest reference. This closes the TOCTOU window
	// between verify and pull: a mutable tag could be rewritten on the registry
	// after verification but before the Docker daemon pulls. Pinning to the
	// verified digest ensures both operations target the same manifest.
	digest, err := extractCosignDigest(out)
	if err != nil {
		return "", fmt.Errorf("image verified but could not extract manifest digest (is cosign up to date?): %w", err)
	}

	pinned := pinnedImageRef(image, digest)
	slog.Info("docker: image signature verified", "image", image, "pinned_ref", pinned)
	return pinned, nil
}

// cosignSig is the subset of cosign's JSON output that we need.
type cosignSig struct {
	Critical struct {
		Image struct {
			Digest string `json:"docker-manifest-digest"`
		} `json:"image"`
	} `json:"critical"`
}

// extractCosignDigest finds the JSON array in cosign's combined output and
// returns the manifest digest of the first verified signature entry.
func extractCosignDigest(out []byte) (string, error) {
	for _, line := range bytes.Split(out, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte("[")) {
			continue
		}
		var sigs []cosignSig
		if err := json.Unmarshal(line, &sigs); err != nil {
			continue
		}
		if len(sigs) == 0 {
			continue
		}
		d := sigs[0].Critical.Image.Digest
		if d == "" {
			continue
		}
		if !strings.HasPrefix(d, "sha256:") {
			return "", fmt.Errorf("unexpected digest algorithm in cosign output: %q", d)
		}
		return d, nil
	}
	return "", fmt.Errorf("manifest digest not found in cosign output")
}

// pinnedImageRef strips any existing tag from image and appends @digest,
// returning an immutable digest-pinned reference.
//
// Examples:
//
//	"python:3.12-slim", "sha256:abc" → "python@sha256:abc"
//	"registry:5000/foo:v1", "sha256:abc" → "registry:5000/foo@sha256:abc"
//	"myimage", "sha256:abc" → "myimage@sha256:abc"
func pinnedImageRef(image, digest string) string {
	// Strip any pre-existing digest.
	if i := strings.Index(image, "@"); i != -1 {
		image = image[:i]
	}
	// Find the last ':' that follows the last '/' — that is the tag separator,
	// not a port number embedded in a registry hostname.
	lastSlash := strings.LastIndex(image, "/")
	if tagIdx := strings.LastIndex(image[lastSlash+1:], ":"); tagIdx != -1 {
		image = image[:lastSlash+1+tagIdx]
	}
	return image + "@" + digest
}
