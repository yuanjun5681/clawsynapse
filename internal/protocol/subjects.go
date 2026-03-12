package protocol

import (
	"regexp"
	"strings"
)

var subjectRe = regexp.MustCompile(`^clawsynapse\.(auth|trust|discovery|control|msg|events|pubsub|transfer)\.[a-z0-9-]+(\.[a-z0-9-]+){1,4}$`)

func ValidateSubject(subject string) error {
	if !subjectRe.MatchString(subject) {
		return NewError(ErrInvalidSubject, "subject format is invalid")
	}
	return nil
}

func SubjectModule(subject string) (string, error) {
	if err := ValidateSubject(subject); err != nil {
		return "", err
	}
	parts := strings.Split(subject, ".")
	if len(parts) < 4 {
		return "", NewError(ErrInvalidSubject, "subject has insufficient segments")
	}
	return parts[1], nil
}

func SubjectTarget(subject string) (string, error) {
	if err := ValidateSubject(subject); err != nil {
		return "", err
	}
	parts := strings.Split(subject, ".")
	if len(parts) < 4 {
		return "", NewError(ErrInvalidSubject, "subject has insufficient segments")
	}
	return parts[2], nil
}
