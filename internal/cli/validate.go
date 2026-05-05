package cli

import (
	"fmt"
)

func validateSource(value string) error {
	switch value {
	case "git", "jira", "code", "all":
		return nil
	}
	return fmt.Errorf("--source must be one of: git, jira, code, all")
}

func validateStatus(value string) error {
	switch value {
	case "open", "resolved", "all":
		return nil
	}
	return fmt.Errorf("--status must be one of: open, resolved, all")
}

func validateRange(label string, n, min, max int) error {
	if n < min || n > max {
		return fmt.Errorf("%s must be an integer between %d and %d", label, min, max)
	}
	return nil
}
