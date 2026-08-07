package missions

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

var slugRe = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

const (
	maxSteps          = 50
	maxStepNameRunes  = 120
	maxToolNameRunes  = 120
	maxSlugLen        = 100
	maxNameLen        = 200
	maxDescriptionLen = 4000
)

func (r CreateMissionRequest) Validate() error {
	slug := strings.TrimSpace(r.Slug)
	if slug == "" || len(slug) > maxSlugLen || !slugRe.MatchString(slug) {
		return fmt.Errorf("%w: invalid slug", ErrInvalidMission)
	}
	if strings.TrimSpace(r.Name) == "" || utf8.RuneCountInString(r.Name) > maxNameLen {
		return fmt.Errorf("%w: invalid name", ErrInvalidMission)
	}
	if utf8.RuneCountInString(r.Description) > maxDescriptionLen {
		return fmt.Errorf("%w: description too long", ErrInvalidMission)
	}
	return validateSteps(r.Steps)
}

func validateSteps(steps []Step) error {
	if len(steps) == 0 {
		return fmt.Errorf("%w: at least one step required", ErrInvalidMission)
	}
	if len(steps) > maxSteps {
		return fmt.Errorf("%w: too many steps (max %d)", ErrInvalidMission, maxSteps)
	}
	for i, s := range steps {
		if strings.TrimSpace(s.Tool) == "" || utf8.RuneCountInString(s.Tool) > maxToolNameRunes {
			return fmt.Errorf("%w: step %d has invalid tool", ErrInvalidMission, i)
		}
		if utf8.RuneCountInString(s.Name) > maxStepNameRunes {
			return fmt.Errorf("%w: step %d name too long", ErrInvalidMission, i)
		}
	}
	return nil
}
