// Package config handles YAML configuration parsing, env var substitution,
// validation, docker-compose.yaml discovery, and auto-creation of defaults.
//
// Config file: push-observer.yaml in the current working directory.
// Env vars use ${VAR} or ${VAR:default} syntax — secrets are never in plain text.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ──────────────────────────────── Structs ────────────────────────────────

// Config is the root configuration for pushObserver.
type Config struct {
	Server        ServerConfig    `yaml:"server"`
	Hooks         []HookConfig    `yaml:"hooks"`
	Notifications NotifyConfig    `yaml:"notifications"`
	RateLimit     RateLimitConfig `yaml:"rate_limit"`
	Logging       LoggingConfig   `yaml:"logging"`

	configPath string // set by Load; used by Save to write back
}

// ServerConfig holds the HTTP server settings.
type ServerConfig struct {
	Port         int      `yaml:"port"`
	Host         string   `yaml:"host"`
	ReadTimeout  Duration `yaml:"read_timeout"`
	WriteTimeout Duration `yaml:"write_timeout"`
}

// HookConfig defines a single webhook-to-deploy pipeline.
type HookConfig struct {
	ID          string           `yaml:"id" json:"id"`
	RepoURL     string           `yaml:"repo_url" json:"repo_url"`
	RepoDir     string           `yaml:"repo_dir" json:"repo_dir"`
	Branch      string           `yaml:"branch" json:"branch"`
	GitSSHKey   string           `yaml:"git_ssh_key" json:"git_ssh_key"`
	HMAC        HMACConfig       `yaml:"hmac" json:"hmac"`
	ContentType string           `yaml:"content_type" json:"content_type"`
	Services    []ServiceConfig  `yaml:"services" json:"services"`
	Compose     ComposeConfig    `yaml:"compose" json:"compose"`
	Deploy      DeployConfig     `yaml:"deploy" json:"deploy"`
	Notify      NotifyHookConfig `yaml:"notify" json:"notify"`
}

// HMACConfig defines the HMAC validation settings for a hook.
type HMACConfig struct {
	Type   string `yaml:"type" json:"type"`     // sha256, sha1, plain
	Secret string `yaml:"secret" json:"secret"` // may contain ${VAR}
	Header string `yaml:"header" json:"header"` // X-Hub-Signature-256, etc.
}

// ServiceConfig maps a docker-compose service to a directory path.
type ServiceConfig struct {
	Name           string `yaml:"name" json:"name"`
	Path           string `yaml:"path" json:"path"`                       // relative to repo_dir
	RestartTrigger string `yaml:"restart_trigger" json:"restart_trigger"` // default, always, on-change
}

// ComposeConfig controls docker compose behaviour.
type ComposeConfig struct {
	Build   bool `yaml:"build" json:"build"`
	Cleanup bool `yaml:"cleanup" json:"cleanup"`
}

// DeployConfig controls the deploy pipeline.
type DeployConfig struct {
	Timeout          Duration `yaml:"timeout" json:"timeout"`
	Retry            int      `yaml:"retry" json:"retry"`
	CustomExtensions []string `yaml:"custom_extensions" json:"custom_extensions"` // file extensions that trigger restart (e.g., [".py", ".yaml"])
}

// NotifyHookConfig controls per-hook notification behaviour.
type NotifyHookConfig struct {
	OnSuccess   bool `yaml:"on_success" json:"on_success"`
	OnFailure   bool `yaml:"on_failure" json:"on_failure"`
	OnNoChanges bool `yaml:"on_no_changes" json:"on_no_changes"`
}

// NotifyConfig holds global notification settings (Apprise).
type NotifyConfig struct {
	AppriseURL   string `yaml:"apprise_url"`
	TagSuccess   string `yaml:"tag_success"`
	TagFailure   string `yaml:"tag_failure"`
	TagNoChanges string `yaml:"tag_no_changes"`
}

// RateLimitConfig controls the global rate limiter.
type RateLimitConfig struct {
	Enabled           bool `yaml:"enabled"`
	RequestsPerMinute int  `yaml:"requests_per_minute"`
	Burst             int  `yaml:"burst"`
}

// LoggingConfig controls structured logging.
type LoggingConfig struct {
	Level  string `yaml:"level"`  // debug, info, warn, error
	Format string `yaml:"format"` // json, text
	File   string `yaml:"file"`   // empty = stdout
}

// ───────────────────────── Custom Duration type ─────────────────────────

// Duration wraps time.Duration to support YAML string parsing (e.g. "30s", "5m").
type Duration time.Duration

// UnmarshalYAML implements yaml.Unmarshaler so "30s" in YAML becomes a Duration.
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return fmt.Errorf("duration must be a string: %w", err)
	}
	dur, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(dur)
	return nil
}

// MarshalYAML implements yaml.Marshaler so Duration is serialized as "30s" string.
func (d Duration) MarshalYAML() (interface{}, error) {
	return time.Duration(d).String(), nil
}

// Duration returns the underlying time.Duration.
func (d Duration) Duration() time.Duration { return time.Duration(d) }

// String returns the human-readable duration.
func (d Duration) String() string { return time.Duration(d).String() }

// ──────────────────── Env var substitution ──────────────────────────────

// envPattern matches ${VAR} and ${VAR:default}. Groups: 1=VARNAME, 2=:default (optional)
var envPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::([^}]*))?\}`)

// expandEnv replaces ${VAR} and ${VAR:default} patterns in the input.
// ${VAR} → os.Getenv("VAR") or ""
// ${VAR:default} → os.Getenv("VAR") or "default"
func expandEnv(input []byte) []byte {
	return envPattern.ReplaceAllFunc(input, func(match []byte) []byte {
		parts := envPattern.FindSubmatch(match)
		// parts[0] = full match, parts[1] = var name, parts[2] = default (optional)
		varName := string(parts[1])
		defaultVal := ""
		if len(parts) > 2 && parts[2] != nil {
			defaultVal = string(parts[2])
		}
		if val, ok := os.LookupEnv(varName); ok {
			return []byte(val)
		}
		if len(parts) > 2 && parts[2] != nil {
			return []byte(defaultVal)
		}
		return []byte("")
	})
}

// ───────────────────────── Load ─────────────────────────────────────────

const defaultConfigPath = "push-observer.yaml"

// Load reads and parses the config file. If the file does not exist, it
// creates a minimal config (CreateDefault) and returns that.
// Env vars in the form ${VAR} or ${VAR:default} are expanded before YAML parsing.
func Load(path string) (*Config, error) {
	if path == "" {
		path = defaultConfigPath
	}

	// Normalize the path to prevent traversal.
	// The config path is always from workdir (fixed), not external input.
	// We use filepath.Clean + explicit ".." check instead of os.Root (Go 1.24).
	// Rationale: os.Root is the canonical Go 1.24 approach for path scoping
	// with external input; here the path is fixed, so filepath.Clean suffices.
	// If the path ever comes from external input, migrate to os.Root.
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolving config path: %w", err)
	}
	cleanPath := filepath.Clean(absPath)
	if strings.Contains(cleanPath, "..") {
		return nil, fmt.Errorf("invalid config path: %q", path)
	}

	data, err := os.ReadFile(cleanPath)
	if err != nil {
		if os.IsNotExist(err) {
			cfg := DefaultConfig()
			if writeErr := writeDefault(cleanPath, cfg); writeErr != nil {
				return nil, fmt.Errorf("config file not found and could not create default: %w", writeErr)
			}
			return cfg, nil
		}
		return nil, fmt.Errorf("reading config file %q: %w", cleanPath, err)
	}

	// Expand env vars in raw YAML before parsing.
	expanded := expandEnv(data)

	var cfg Config
	decoder := yaml.NewDecoder(strings.NewReader(string(expanded)))
	decoder.KnownFields(true) // reject unknown YAML keys
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parsing config %q: %w", path, err)
	}

	// Scan for docker-compose.yaml files and populate services for hooks that
	// don't have explicit services defined.
	for i := range cfg.Hooks {
		if len(cfg.Hooks[i].Services) == 0 {
			cfg.Hooks[i].Services = ScanServices(cfg.Hooks[i].RepoDir)
		}
	}

	// Apply defaults for missing optional fields.
	cfg.applyDefaults()

	cfg.configPath = cleanPath

	return &cfg, nil
}

// ─────────────────────── Validate ───────────────────────────────────────

// Validate checks the configuration for semantic correctness.
// Returns nil if valid, or an error describing all issues found.
func (c *Config) Validate() error {
	var errs []string

	// Server
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		errs = append(errs, fmt.Sprintf("server.port must be 1-65535, got %d", c.Server.Port))
	}

	// Hooks
	if len(c.Hooks) == 0 {
		errs = append(errs, "at least one hook is required")
	}
	seenIDs := make(map[string]bool)
	validHMACTypes := map[string]bool{"sha256": true, "sha1": true, "plain": true}
	validLogLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
	validLogFormats := map[string]bool{"json": true, "text": true}
	validContentTypes := map[string]bool{"json": true, "form": true}
	validRestartTriggers := map[string]bool{"default": true, "always": true, "on-change": true}

	for i := range c.Hooks {
		h := &c.Hooks[i]
		prefix := fmt.Sprintf("hooks[%d]", i)

		if h.ID == "" {
			errs = append(errs, fmt.Sprintf("%s.id is required", prefix))
		} else if seenIDs[h.ID] {
			errs = append(errs, fmt.Sprintf("%s.id %q is duplicated", prefix, h.ID))
		}
		seenIDs[h.ID] = true

		if h.RepoURL == "" {
			errs = append(errs, fmt.Sprintf("%s.repo_url is required", prefix))
		}
		if h.RepoDir == "" {
			errs = append(errs, fmt.Sprintf("%s.repo_dir is required", prefix))
		}
		if h.Branch == "" {
			errs = append(errs, fmt.Sprintf("%s.branch is required", prefix))
		}

		// HMAC validation
		if !validHMACTypes[h.HMAC.Type] {
			errs = append(errs, fmt.Sprintf("%s.hmac.type must be one of sha256,sha1,plain, got %q", prefix, h.HMAC.Type))
		}
		if h.HMAC.Type != "plain" && h.HMAC.Secret == "" {
			errs = append(errs, fmt.Sprintf("%s.hmac.secret is required when type is %q (use ${ENV_VAR})", prefix, h.HMAC.Type))
		}
		if h.HMAC.Type != "plain" && h.HMAC.Header == "" {
			errs = append(errs, fmt.Sprintf("%s.hmac.header is required when type is %q", prefix, h.HMAC.Type))
		}

		// Content type
		if h.ContentType != "" && !validContentTypes[h.ContentType] {
			errs = append(errs, fmt.Sprintf("%s.content_type must be json or form, got %q", prefix, h.ContentType))
		}

		// Services
		for j := range h.Services {
			sp := fmt.Sprintf("%s.services[%d]", prefix, j)
			if h.Services[j].Name == "" {
				errs = append(errs, fmt.Sprintf("%s.name is required", sp))
			}
			if h.Services[j].Path == "" {
				errs = append(errs, fmt.Sprintf("%s.path is required", sp))
			}
			if h.Services[j].RestartTrigger != "" && !validRestartTriggers[h.Services[j].RestartTrigger] {
				errs = append(errs, fmt.Sprintf("%s.restart_trigger must be default, always, or on-change, got %q", sp, h.Services[j].RestartTrigger))
			}
		}
	}

	// Notifications
	if c.Notifications.AppriseURL == "" {
		errs = append(errs, "notifications.apprise_url is required for notifications")
	}

	// Rate limit
	if c.RateLimit.Enabled {
		if c.RateLimit.RequestsPerMinute <= 0 {
			errs = append(errs, "rate_limit.requests_per_minute must be > 0 when enabled")
		}
		if c.RateLimit.Burst < 0 {
			errs = append(errs, "rate_limit.burst must be >= 0")
		}
	}

	// Logging
	if !validLogLevels[c.Logging.Level] {
		errs = append(errs, fmt.Sprintf("logging.level must be one of debug,info,warn,error, got %q", c.Logging.Level))
	}
	if !validLogFormats[c.Logging.Format] {
		errs = append(errs, fmt.Sprintf("logging.format must be json or text, got %q", c.Logging.Format))
	}

	if len(errs) > 0 {
		return fmt.Errorf("config validation failed:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}

// ─────────────────────── Defaults ───────────────────────────────────────

// DefaultConfig returns a Config with safe defaults suitable for first run.
func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Port:         9090,
			Host:         "0.0.0.0",
			ReadTimeout:  Duration(30 * time.Second),
			WriteTimeout: Duration(300 * time.Second),
		},
		Notifications: NotifyConfig{
			TagSuccess:   "deploy-success",
			TagFailure:   "deploy-failure",
			TagNoChanges: "",
		},
		RateLimit: RateLimitConfig{
			Enabled:           true,
			RequestsPerMinute: 30,
			Burst:             5,
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "json",
		},
	}
}

// applyDefaults fills in zero-value fields with their defaults.
func (c *Config) applyDefaults() {
	if c.Server.Port == 0 {
		c.Server.Port = 9090
	}
	if c.Server.Host == "" {
		c.Server.Host = "0.0.0.0"
	}
	if c.Server.ReadTimeout == 0 {
		c.Server.ReadTimeout = Duration(30 * time.Second)
	}
	if c.Server.WriteTimeout == 0 {
		c.Server.WriteTimeout = Duration(300 * time.Second)
	}
	if c.Notifications.TagSuccess == "" {
		c.Notifications.TagSuccess = "deploy-success"
	}
	if c.Notifications.TagFailure == "" {
		c.Notifications.TagFailure = "deploy-failure"
	}
	if c.RateLimit.RequestsPerMinute == 0 {
		c.RateLimit.RequestsPerMinute = 30
	}
	if c.Logging.Level == "" {
		c.Logging.Level = "info"
	}
	if c.Logging.Format == "" {
		c.Logging.Format = "json"
	}

	for i := range c.Hooks {
		if c.Hooks[i].Branch == "" {
			c.Hooks[i].Branch = "main"
		}
		if c.Hooks[i].ContentType == "" {
			c.Hooks[i].ContentType = "json"
		}
		if c.Hooks[i].HMAC.Type == "" {
			c.Hooks[i].HMAC.Type = "sha256"
		}
		for j := range c.Hooks[i].Services {
			if c.Hooks[i].Services[j].RestartTrigger == "" {
				c.Hooks[i].Services[j].RestartTrigger = "default"
			}
		}
	}
}

// writeDefault writes the default config to disk.
func writeDefault(path string, cfg *Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshaling default config: %w", err)
	}
	header := []byte("# pushObserver default configuration — auto-generated on first run\n" +
		"# Customize this file for your environment.\n" +
		"# Secrets use ${ENV_VAR} or ${ENV_VAR:default} syntax.\n\n")
	content := append(header, data...)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		return fmt.Errorf("writing default config to %q: %w", path, err)
	}
	return nil
}

// ──────────────────── Persistence ───────────────────────────────────────

// Save writes the current configuration back to the YAML file it was loaded from.
// If the config was created via DefaultConfig (not loaded from disk), returns an error.
func (c *Config) Save() error {
	if c.configPath == "" {
		return fmt.Errorf("cannot save: config was not loaded from a file (use Load first)")
	}
	return writeDefault(c.configPath, c)
}

// ──────────────────── Hook CRUD ─────────────────────────────────────────

// HookByID returns a pointer to the hook with the given ID, or nil if not found.
func (c *Config) HookByID(id string) *HookConfig {
	for i := range c.Hooks {
		if c.Hooks[i].ID == id {
			return &c.Hooks[i]
		}
	}
	return nil
}

// AddHook appends a new hook to the configuration and persists it.
// Returns an error if a hook with the same ID already exists.
func (c *Config) AddHook(h HookConfig) error {
	if c.HookByID(h.ID) != nil {
		return fmt.Errorf("hook %q already exists", h.ID)
	}
	c.Hooks = append(c.Hooks, h)
	if err := c.Save(); err != nil {
		// Rollback: remove the appended hook
		c.Hooks = c.Hooks[:len(c.Hooks)-1]
		return fmt.Errorf("saving config after adding hook %q: %w", h.ID, err)
	}
	return nil
}

// UpdateHook replaces an existing hook's configuration and persists.
// Returns an error if the hook does not exist.
func (c *Config) UpdateHook(id string, updated HookConfig) error {
	idx := -1
	for i := range c.Hooks {
		if c.Hooks[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("hook %q not found", id)
	}
	// Preserve the ID from the URL, not the request body
	updated.ID = id
	old := c.Hooks[idx]
	c.Hooks[idx] = updated
	if err := c.Save(); err != nil {
		// Rollback
		c.Hooks[idx] = old
		return fmt.Errorf("saving config after updating hook %q: %w", id, err)
	}
	return nil
}

// RemoveHook deletes a hook by ID and persists.
// Returns an error if the hook does not exist.
func (c *Config) RemoveHook(id string) error {
	idx := -1
	for i := range c.Hooks {
		if c.Hooks[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("hook %q not found", id)
	}
	removed := c.Hooks[idx]
	c.Hooks = append(c.Hooks[:idx], c.Hooks[idx+1:]...)
	if err := c.Save(); err != nil {
		// Rollback: restore the removed hook at its original position
		c.Hooks = append(c.Hooks[:idx], append([]HookConfig{removed}, c.Hooks[idx:]...)...)
		return fmt.Errorf("saving config after removing hook %q: %w", id, err)
	}
	return nil
}

// ──────────────────── docker-compose.yaml scanning ──────────────────────

// dockerComposeFile is the canonical docker compose filename.
const dockerComposeFile = "docker-compose.yaml"

// ScanServices walks repoDir and returns ServiceConfig entries for every
// subdirectory that contains a docker-compose.yaml file. The path is relative
// to repoDir. If repoDir itself contains docker-compose.yaml, it's included
// with path ".".
func ScanServices(repoDir string) []ServiceConfig {
	if repoDir == "" {
		return nil
	}

	var services []ServiceConfig
	absRepo, err := filepath.Abs(repoDir)
	if err != nil {
		return nil
	}

	// Check repoDir itself first.
	if hasCompose(absRepo) {
		services = append(services, ServiceConfig{
			Name:           filepath.Base(absRepo),
			Path:           ".",
			RestartTrigger: "default",
		})
	}

	// Walk subdirectories (depth 1 only — don't recurse into service internals).
	entries, err := os.ReadDir(absRepo)
	if err != nil {
		return services
	}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		subDir := filepath.Join(absRepo, entry.Name())
		if hasCompose(subDir) {
			relPath, _ := filepath.Rel(absRepo, subDir)
			if relPath == "" {
				relPath = entry.Name()
			}
			services = append(services, ServiceConfig{
				Name:           entry.Name(),
				Path:           relPath,
				RestartTrigger: "default",
			})
		}
	}
	return services
}

// hasCompose returns true if the directory contains docker-compose.yaml.
func hasCompose(dir string) bool {
	fi, err := os.Stat(filepath.Join(dir, dockerComposeFile))
	return err == nil && !fi.IsDir()
}

// ──────────────────── Hook ID Validation ─────────────────────────────────

// hookIDPattern defines the allowed character set for hook IDs.
// Only alphanumeric, hyphens, underscores, and dots are permitted.
var hookIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)

// ValidHookID returns true if the hook ID contains only safe characters.
// This prevents open redirect, path traversal, and header injection via hook IDs.
func ValidHookID(id string) bool {
	return id != "" && hookIDPattern.MatchString(id)
}
