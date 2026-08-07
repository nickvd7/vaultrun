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
	maxVersionLen     = 20
	maxTags           = 20
	maxTagLen         = 64
	maxArgValueLen    = 4096
)

func (r CreateMissionRequest) Validate() error {
	slug := strings.TrimSpace(r.Slug)
	if slug == "" || len(slug) > maxSlugLen || !slugRe.MatchString(slug) {
		return fmt.Errorf("%w: invalid slug", ErrInvalidMission)
	}
	r.Slug = slug
	name := strings.TrimSpace(r.Name)
	if name == "" || utf8.RuneCountInString(name) > maxNameLen {
		return fmt.Errorf("%w: invalid name", ErrInvalidMission)
	}
	r.Name = name
	if utf8.RuneCountInString(r.Description) > maxDescriptionLen {
		return fmt.Errorf("%w: description too long", ErrInvalidMission)
	}
	if r.Version != "" && utf8.RuneCountInString(r.Version) > maxVersionLen {
		return fmt.Errorf("%w: version too long", ErrInvalidMission)
	}
	if err := validateTags(r.Tags); err != nil {
		return err
	}
	return validateSteps(r.Steps)
}

func validateTags(tags []string) error {
	if len(tags) > maxTags {
		return fmt.Errorf("%w: too many tags (max %d)", ErrInvalidMission, maxTags)
	}
	for _, t := range tags {
		if utf8.RuneCountInString(t) > maxTagLen {
			return fmt.Errorf("%w: tag too long", ErrInvalidMission)
		}
	}
	return nil
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
		for k, v := range s.Args {
			if len(k) > maxToolNameRunes || len(v) > maxArgValueLen {
				return fmt.Errorf("%w: step %d has oversized args", ErrInvalidMission, i)
			}
		}
	}
	return nil
}

// NormalizeCreate trims slug/name after Validate for persistence.
func (r *CreateMissionRequest) Normalize() {
	r.Slug = strings.TrimSpace(r.Slug)
	r.Name = strings.TrimSpace(r.Name)
}
