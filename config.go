package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type FileConfig struct {
	LLM    LLMConfig    `yaml:"llm"`
	Create CreateConfig `yaml:"create"`
}

type LLMConfig struct {
	Provider string `yaml:"provider"`
	Endpoint string `yaml:"endpoint,omitempty"`
	Model    string `yaml:"model"`
}

type CreateConfig struct {
	Draft       *bool `yaml:"draft"`
	SkipConfirm *bool `yaml:"skip_confirm"`
}

type loadedConfig struct {
	Config  FileConfig
	Sources []string
}

func loadConfig(ctx context.Context, debug bool, errOut io.Writer) (loadedConfig, error) {
	var out loadedConfig

	// Optional: explicit config path override (still below env var precedence).
	if p := strings.TrimSpace(os.Getenv("GH_PR_SUGGEST_CONFIG")); p != "" {
		cfg, err := readYAMLConfigFile(p)
		if err != nil {
			return loadedConfig{}, err
		}
		out.Config = mergeFileConfig(out.Config, cfg)
		out.Sources = append(out.Sources, p)
	}

	// Global config path resolution (requested):
	// 1) GH_PR_SUGGEST_CONFIG (already handled above)
	// 2) XDG_CONFIG_HOME/gh-dash/config.yml
	// 3) $HOME/.config/gh-dash/config.yml
	if strings.TrimSpace(os.Getenv("GH_PR_SUGGEST_CONFIG")) == "" {
		if p := defaultGlobalConfigPath(); p != "" {
			if cfg, ok, err := readYAMLConfigFileIfExists(p); err != nil {
				return loadedConfig{}, err
			} else if ok {
				out.Config = mergeFileConfig(out.Config, cfg)
				out.Sources = append(out.Sources, p)
			}
		}
	}

	// Repo-level config files.
	if repoRoot, err := gitRepoRoot(ctx); err == nil && repoRoot != "" {
		candidates := []string{
			filepath.Join(repoRoot, ".github", "gh-pr-suggest.yml"),
			filepath.Join(repoRoot, ".github", "gh-pr-suggest.yaml"),
			filepath.Join(repoRoot, ".gh-pr-suggest.yml"),
			filepath.Join(repoRoot, ".gh-pr-suggest.yaml"),
		}
		for _, p := range candidates {
			if cfg, ok, err := readYAMLConfigFileIfExists(p); err != nil {
				return loadedConfig{}, err
			} else if ok {
				out.Config = mergeFileConfig(out.Config, cfg)
				out.Sources = append(out.Sources, p)
				// Prefer the first match (more explicit path first).
				break
			}
		}
	}

	if debug && errOut != nil && len(out.Sources) > 0 {
		fmt.Fprintf(errOut, "[debug] loaded config files:\n")
		for _, s := range out.Sources {
			fmt.Fprintf(errOut, "[debug] - %s\n", s)
		}
	}
	return out, nil
}

func defaultGlobalConfigPath() string {
	if d := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); d != "" {
		return filepath.Join(d, "gh-dash", "config.yml")
	}
	if h, err := os.UserHomeDir(); err == nil && strings.TrimSpace(h) != "" {
		return filepath.Join(h, ".config", "gh-dash", "config.yml")
	}
	return ""
}

func gitRepoRoot(ctx context.Context) (string, error) {
	out, err := runGit(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func readYAMLConfigFileIfExists(path string) (FileConfig, bool, error) {
	if strings.TrimSpace(path) == "" {
		return FileConfig{}, false, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return FileConfig{}, false, nil
		}
		return FileConfig{}, false, fmt.Errorf("failed to stat config file %s: %w", path, err)
	}
	if info.IsDir() {
		return FileConfig{}, false, fmt.Errorf("config path is a directory: %s", path)
	}
	cfg, err := readYAMLConfigFile(path)
	if err != nil {
		return FileConfig{}, false, err
	}
	return cfg, true, nil
}

func readYAMLConfigFile(path string) (FileConfig, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return FileConfig{}, fmt.Errorf("failed to read config file %s: %w", path, err)
	}
	var cfg FileConfig
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return FileConfig{}, fmt.Errorf("failed to parse YAML config file %s: %w", path, err)
	}
	return cfg, nil
}

func mergeFileConfig(base FileConfig, overlay FileConfig) FileConfig {
	// Strings: overlay wins if non-empty.
	if strings.TrimSpace(overlay.LLM.Provider) != "" {
		base.LLM.Provider = strings.TrimSpace(overlay.LLM.Provider)
	}
	if strings.TrimSpace(overlay.LLM.Endpoint) != "" {
		base.LLM.Endpoint = strings.TrimSpace(overlay.LLM.Endpoint)
	}
	if strings.TrimSpace(overlay.LLM.Model) != "" {
		base.LLM.Model = strings.TrimSpace(overlay.LLM.Model)
	}

	// Bools: overlay wins if explicitly set.
	if overlay.Create.Draft != nil {
		base.Create.Draft = overlay.Create.Draft
	}
	if overlay.Create.SkipConfirm != nil {
		base.Create.SkipConfirm = overlay.Create.SkipConfirm
	}
	return base
}

func configCreationPath() (string, error) {
	// 1) GH_PR_SUGGEST_CONFIG if present
	if p := strings.TrimSpace(os.Getenv("GH_PR_SUGGEST_CONFIG")); p != "" {
		return p, nil
	}
	// 2) XDG_CONFIG_HOME/gh-dash/config.yml
	// 3) $HOME/.config/gh-dash/config.yml
	p := defaultGlobalConfigPath()
	if strings.TrimSpace(p) == "" {
		return "", fmt.Errorf("could not determine a global config path")
	}
	return p, nil
}

func writeYAMLConfigFile(path string, cfg FileConfig) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("config path is empty")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create config directory %s: %w", dir, err)
	}
	b, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal YAML: %w", err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("failed to write config file %s: %w", path, err)
	}
	return nil
}

