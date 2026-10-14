package docker

import (
	"errors"
	"testing"
)

func TestIsImageUnavailable(t *testing.T) {
	cases := map[string]bool{
		"Error response from daemon: invalid reference format: repository name must be lowercase": true,
		"pull access denied for nope, repository does not exist or may require authorization":     true,
		"manifest unknown":                    true,
		"Cannot connect to the Docker daemon": false,
		"context deadline exceeded":           false,
	}
	for msg, want := range cases {
		if got := isImageUnavailable(errors.New(msg)); got != want {
			t.Errorf("isImageUnavailable(%q) = %v, want %v", msg, got, want)
		}
	}
}
