package templates

import (
	"fmt"
	"regexp"
	"strings"
)

// Validation bounds. These are deliberately generous — they exist to reject
// values that would break the Docker API or exhaust the host, not to express
// a per-deployment quota (that belongs in the policy layer).
const (
	MaxCPULimit       = 64.0
	MaxMemoryLimitMB  = 262144 // 256 GB
	MaxTimeoutSeconds = 86400  // 24 hours
	MaxSlugLength     = 100
	MaxNameLength     = 200
	MaxTagCount       = 20
	MaxAllowedHosts   = 100
	MaxEnvVarCount    = 100
	MaxStartupScript  = 64 * 1024 // 64 KB
)

// slugPattern restricts slugs to lowercase alphanumerics and single hyphens.
// Slugs appear in URLs, so anything else invites path-confusion bugs.
var slugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// imageRefPattern matches a Docker image reference: optional registry host,
// path segments, and an optional :tag or @sha256: digest.
var imageRefPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._\-/:]*(@sha256:[a-f0-9]{64})?$`)

// envKeyPattern matches POSIX environment variable names.
var envKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Validate checks a create request before it reaches the database. Returning
// all problems at once rather than the first keeps the API usable for callers
// building templates programmatically.
func (r CreateTemplateRequest) Validate() error {
	var problems []string

	if err := validateSlug(r.Slug); err != nil {
		problems = append(problems, err.Error())
	}
	if err := validateName(r.Name); err != nil {
		problems = append(problems, err.Error())
	}
	if strings.TrimSpace(r.Description) == "" {
		problems = append(problems, "description must not be empty")
	}
	if strings.TrimSpace(r.Category) == "" {
		problems = append(problems, "category must not be empty")
	}
	if err := validateImage(r.Image); err != nil {
		problems = append(problems, err.Error())
	}
	if err := validateResources(r.Resources); err != nil {
		problems = append(problems, err.Error())
	}
	if err := validateNetwork(r.Network); err != nil {
		problems = append(problems, err.Error())
	}
	if err := validateTags(r.Tags); err != nil {
		problems = append(problems, err.Error())
	}
	if err := validateEnvironment(r.Environment); err != nil {
		problems = append(problems, err.Error())
	}
	if len(r.StartupScript) > MaxStartupScript {
		problems = append(problems, fmt.Sprintf(
			"startup_script is %d bytes, maximum is %d", len(r.StartupScript), MaxStartupScript))
	}

	if len(problems) > 0 {
		return fmt.Errorf("%w: %s", ErrInvalidTemplate, strings.Join(problems, "; "))
	}
	return nil
}

// Validate checks the mutable fields of an update request. Only non-nil fields
// are checked, so a partial update cannot be rejected for a field it omits.
func (r UpdateTemplateRequest) Validate() error {
	var problems []string

	if r.Name != nil {
		if err := validateName(*r.Name); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if r.Description != nil && strings.TrimSpace(*r.Description) == "" {
		problems = append(problems, "description must not be empty")
	}
	if r.Image != nil {
		if err := validateImage(*r.Image); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if r.Resources != nil {
		if err := validateResources(*r.Resources); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if r.Network != nil {
		if err := validateNetwork(*r.Network); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if r.Tags != nil {
		if err := validateTags(r.Tags); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if r.Environment != nil {
		if err := validateEnvironment(r.Environment); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if r.StartupScript != nil && len(*r.StartupScript) > MaxStartupScript {
		problems = append(problems, fmt.Sprintf(
			"startup_script is %d bytes, maximum is %d", len(*r.StartupScript), MaxStartupScript))
	}

	if len(problems) > 0 {
		return fmt.Errorf("%w: %s", ErrInvalidTemplate, strings.Join(problems, "; "))
	}
	return nil
}

func validateSlug(slug string) error {
	if slug == "" {
		return fmt.Errorf("slug must not be empty")
	}
	if len(slug) > MaxSlugLength {
		return fmt.Errorf("slug is %d characters, maximum is %d", len(slug), MaxSlugLength)
	}
	if !slugPattern.MatchString(slug) {
		return fmt.Errorf("slug %q must be lowercase alphanumerics separated by single hyphens", slug)
	}
	return nil
}

func validateName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("name must not be empty")
	}
	if len(name) > MaxNameLength {
		return fmt.Errorf("name is %d characters, maximum is %d", len(name), MaxNameLength)
	}
	return nil
}

// validateImage rejects anything that is not a plain Docker image reference.
// A template image is passed to the Docker daemon, so a URL or filesystem path
// here would either fail confusingly or, on some daemon configurations, pull
// from an unintended source.
func validateImage(image string) error {
	if image == "" {
		return fmt.Errorf("image must not be empty")
	}
	if len(image) > 500 {
		return fmt.Errorf("image reference is %d characters, maximum is 500", len(image))
	}

	// Reject URL-like references explicitly: the generic pattern below would
	// otherwise accept "http://evil/x" because ':' and '/' are legal in refs.
	for _, scheme := range []string{"http://", "https://", "file://", "docker://", "oci://", "ftp://"} {
		if strings.HasPrefix(strings.ToLower(image), scheme) {
			return fmt.Errorf("image %q must be a Docker reference, not a %s URL", image, strings.TrimSuffix(scheme, "://"))
		}
	}

	if strings.ContainsAny(image, " \t\n\r\x00") {
		return fmt.Errorf("image reference must not contain whitespace or NUL")
	}

	// Shell metacharacters cannot reach a shell (the Docker API is used
	// directly), but rejecting them keeps the value safe for logs and any
	// future code path that does build a command line.
	if strings.ContainsAny(image, "$;|&`<>(){}[]!*?\"'\\") {
		return fmt.Errorf("image reference %q contains shell metacharacters", image)
	}

	// Path traversal in an image reference is always a mistake.
	if strings.Contains(image, "..") {
		return fmt.Errorf("image reference %q must not contain '..'", image)
	}

	if !imageRefPattern.MatchString(image) {
		return fmt.Errorf("image reference %q is not a valid Docker image name", image)
	}

	return nil
}

func validateResources(r ResourceConfig) error {
	if r.CPULimit <= 0 {
		return fmt.Errorf("resources.cpu_limit must be greater than 0, got %g", r.CPULimit)
	}
	if r.CPULimit > MaxCPULimit {
		return fmt.Errorf("resources.cpu_limit is %g, maximum is %g", r.CPULimit, MaxCPULimit)
	}
	if r.MemoryLimitMB <= 0 {
		return fmt.Errorf("resources.memory_limit_mb must be greater than 0, got %d", r.MemoryLimitMB)
	}
	if r.MemoryLimitMB > MaxMemoryLimitMB {
		return fmt.Errorf("resources.memory_limit_mb is %d, maximum is %d", r.MemoryLimitMB, MaxMemoryLimitMB)
	}
	if r.TimeoutSeconds <= 0 {
		return fmt.Errorf("resources.timeout_seconds must be greater than 0, got %d", r.TimeoutSeconds)
	}
	if r.TimeoutSeconds > MaxTimeoutSeconds {
		return fmt.Errorf("resources.timeout_seconds is %d, maximum is %d", r.TimeoutSeconds, MaxTimeoutSeconds)
	}
	return nil
}

// validateNetwork checks the allowed-host list. Hosts are compared against the
// sandbox network policy, so a wildcard or URL here would silently widen access.
func validateNetwork(n NetworkConfig) error {
	if len(n.AllowedHosts) > MaxAllowedHosts {
		return fmt.Errorf("network.allowed_hosts has %d entries, maximum is %d",
			len(n.AllowedHosts), MaxAllowedHosts)
	}

	for _, host := range n.AllowedHosts {
		if host == "" {
			return fmt.Errorf("network.allowed_hosts must not contain empty entries")
		}
		if strings.Contains(host, "://") {
			return fmt.Errorf("network.allowed_hosts entry %q must be a hostname, not a URL", host)
		}
		if strings.ContainsAny(host, " \t\n\r\x00/") {
			return fmt.Errorf("network.allowed_hosts entry %q must not contain whitespace or '/'", host)
		}
		if host == "*" {
			return fmt.Errorf("network.allowed_hosts must not contain '*'; leave the list empty to allow all")
		}
	}

	return nil
}

func validateTags(tags []string) error {
	if len(tags) > MaxTagCount {
		return fmt.Errorf("tags has %d entries, maximum is %d", len(tags), MaxTagCount)
	}
	for _, tag := range tags {
		if strings.TrimSpace(tag) == "" {
			return fmt.Errorf("tags must not contain empty entries")
		}
		if len(tag) > 50 {
			return fmt.Errorf("tag %q is %d characters, maximum is 50", tag, len(tag))
		}
	}
	return nil
}

// validateEnvironment checks env var names. Values are intentionally
// unrestricted (they may legitimately contain any bytes) but names must be
// valid POSIX identifiers so they cannot smuggle extra assignments.
func validateEnvironment(env map[string]string) error {
	if len(env) > MaxEnvVarCount {
		return fmt.Errorf("environment has %d entries, maximum is %d", len(env), MaxEnvVarCount)
	}

	for key := range env {
		if key == "" {
			return fmt.Errorf("environment must not contain an empty variable name")
		}
		if !envKeyPattern.MatchString(key) {
			return fmt.Errorf("environment variable name %q is not a valid identifier", key)
		}
	}

	return nil
}
