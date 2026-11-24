package secrets

import (
	"errors"
	"fmt"
)

// NotFoundError means the backend confirmed that this name has no value.
// Any other error means the backend could not answer (configuration, login,
// network). Callers use that split so a missing name is not reported the
// same way as a Vault or cloud login that failed.
type NotFoundError struct {
	Name string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("secret %q not found", e.Name)
}

// IsNotFound reports whether err is a NotFoundError.
func IsNotFound(err error) bool {
	var n *NotFoundError
	return errors.As(err, &n)
}

// nameError is a rejected secret name. It is not a lookup failure.
type nameError struct{ error }

// IsInvalidName reports whether err is a rejected secret name.
func IsInvalidName(err error) bool {
	var n *nameError
	return errors.As(err, &n)
}
