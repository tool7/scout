package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Jira project keys are documented as uppercase letters, digits, and
// underscores, starting with a letter (e.g. NEWAPP, DES). We enforce that here
// both as a typo guard and to prevent JQL quote injection in
// `project = "<key>"` clauses.
var jiraProjectKeyPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]+$`)

const moduleName = "scout"

type Project struct {
	Name           string   `json:"name"`
	GitPath        string   `json:"gitPath"`
	JiraProjectKey string   `json:"jiraProjectKey"`
	GitRemote      string   `json:"gitRemote"`
	IndexRef       string   `json:"indexRef"`
	ExcludePaths   []string `json:"excludePaths"`
}

type Jira struct {
	Host     string `json:"host"`
	Email    string `json:"email"`
	APIToken string `json:"apiToken"`
}

type Config struct {
	DataDir  string    `json:"dataDir"`
	Jira     Jira      `json:"jira"`
	Projects []Project `json:"projects"`
}

type validationIssue struct {
	path    string
	message string
}

type validationError struct {
	filepath string
	issues   []validationIssue
}

func (e *validationError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Invalid configuration at %s:\n", e.filepath)
	for _, issue := range e.issues {
		path := issue.path
		if path == "" {
			path = "(root)"
		}
		fmt.Fprintf(&b, "  - %s: %s\n", path, issue.message)
	}
	return strings.TrimRight(b.String(), "\n")
}

func Load() (*Config, error) {
	raw, source, err := findConfigSource()
	if err != nil {
		return nil, err
	}

	cfg := &Config{}
	if err := json.Unmarshal(raw, cfg); err != nil {
		return nil, fmt.Errorf("Failed to parse config at %s: %w", source, err)
	}

	if issues := validate(cfg); len(issues) > 0 {
		return nil, &validationError{filepath: source, issues: issues}
	}

	baseDir := filepath.Dir(source)
	cfg.DataDir = resolveFromBase(cfg.DataDir, baseDir)
	for i := range cfg.Projects {
		cfg.Projects[i].GitPath = resolveFromBase(cfg.Projects[i].GitPath, baseDir)
		if cfg.Projects[i].GitRemote == "" {
			cfg.Projects[i].GitRemote = "origin"
		}
		if cfg.Projects[i].ExcludePaths == nil {
			cfg.Projects[i].ExcludePaths = []string{}
		}
	}

	return cfg, nil
}

func findConfigSource() ([]byte, string, error) {
	searchPlaces := []string{
		"scout.config.json",
		".scout.json",
		filepath.Join(".config", moduleName, "config.json"),
	}

	cwd, err := os.Getwd()
	if err != nil {
		return nil, "", fmt.Errorf("failed to read working directory: %w", err)
	}

	dir := cwd
	for {
		for _, name := range searchPlaces {
			candidate := filepath.Join(dir, name)
			if data, err := os.ReadFile(candidate); err == nil {
				return data, candidate, nil
			} else if !errors.Is(err, os.ErrNotExist) {
				return nil, "", fmt.Errorf("failed to read %s: %w", candidate, err)
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	homeConfig, err := homeConfigPath()
	if err != nil {
		return nil, "", err
	}
	if data, err := os.ReadFile(homeConfig); err == nil {
		return data, homeConfig, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, "", fmt.Errorf("failed to read %s: %w", homeConfig, err)
	}

	return nil, "", fmt.Errorf(
		"Configuration not found. Create scout.config.json in the project "+
			"or a config file at %s. See scout.config.example.json for the expected shape.",
		homeConfig,
	)
}

func homeConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve home directory: %w", err)
	}
	return filepath.Join(home, ".scout", "config.json"), nil
}

func expandHome(path string) string {
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return path
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

func resolveFromBase(path, baseDir string) string {
	expanded := expandHome(path)
	if filepath.IsAbs(expanded) {
		return expanded
	}
	return filepath.Join(baseDir, expanded)
}

func validate(cfg *Config) []validationIssue {
	var issues []validationIssue

	if cfg.DataDir == "" {
		issues = append(issues, validationIssue{path: "dataDir", message: "dataDir is required"})
	}

	if cfg.Jira.Host == "" {
		issues = append(issues, validationIssue{path: "jira.host", message: "jira.host must be a valid URL"})
	} else if u, err := url.Parse(cfg.Jira.Host); err != nil || u.Scheme == "" || u.Host == "" {
		issues = append(issues, validationIssue{path: "jira.host", message: "jira.host must be a valid URL"})
	}

	if cfg.Jira.Email == "" {
		issues = append(issues, validationIssue{path: "jira.email", message: "jira.email must be a valid email address"})
	} else if _, err := mail.ParseAddress(cfg.Jira.Email); err != nil {
		issues = append(issues, validationIssue{path: "jira.email", message: "jira.email must be a valid email address"})
	}

	if cfg.Jira.APIToken == "" {
		issues = append(issues, validationIssue{path: "jira.apiToken", message: "jira.apiToken is required"})
	}

	if len(cfg.Projects) == 0 {
		issues = append(issues, validationIssue{path: "projects", message: "at least one project must be configured"})
	}

	for i, p := range cfg.Projects {
		base := fmt.Sprintf("projects.%d", i)
		if p.Name == "" {
			issues = append(issues, validationIssue{path: base + ".name", message: "project name is required"})
		}
		if p.GitPath == "" {
			issues = append(issues, validationIssue{path: base + ".gitPath", message: "gitPath is required"})
		}
		if !jiraProjectKeyPattern.MatchString(p.JiraProjectKey) {
			issues = append(issues, validationIssue{
				path:    base + ".jiraProjectKey",
				message: "jiraProjectKey must match /^[A-Z][A-Z0-9_]+$/ (e.g. NEWAPP, DES)",
			})
		}
		if p.IndexRef != "" && strings.TrimSpace(p.IndexRef) == "" {
			issues = append(issues, validationIssue{path: base + ".indexRef", message: "indexRef must not be empty"})
		}
		for j, pattern := range p.ExcludePaths {
			if pattern == "" {
				issues = append(issues, validationIssue{
					path:    fmt.Sprintf("%s.excludePaths.%d", base, j),
					message: "exclude pattern must not be empty",
				})
			}
		}
	}

	return issues
}
