package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"

	"gopkg.in/yaml.v3"
)

// config is ~/.config/hunch/config.yaml. Flags override environment
// variables, which override the config, which overrides built-in defaults.
// "hunch auth login" writes provider and providers.<id>.model.
//
//	provider: openrouter
//	providers:
//	  openrouter:
//	    model: ~typesafe/jev-latest
//	jobs: 8
//	retries: 4
//	key: hunch
//	presets:
//	  auth:
//	    questions:
//	      auth: { type: noul, q: "Is this an authentication failure?" }
//	    keep: "auth.p > 0.8"
type config struct {
	Provider  string                    `yaml:"provider"`
	Providers map[string]providerConfig `yaml:"providers"`
	Jobs      int                       `yaml:"jobs"`
	Retries   *int                      `yaml:"retries"`
	Key       string                    `yaml:"key"`
	Presets   map[string]*questionSet   `yaml:"presets"`

	path string
}

type providerConfig struct {
	Model   string `yaml:"model"`
	BaseURL string `yaml:"base_url"`
}

func configPath(e *env) string {
	if p := e.getenv("HUNCH_CONFIG"); p != "" {
		return p
	}
	if d := e.getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "hunch", "config.yaml")
	}
	if runtime.GOOS == "windows" {
		if d, err := os.UserConfigDir(); err == nil {
			return filepath.Join(d, "hunch", "config.yaml")
		}
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "hunch", "config.yaml")
}

func loadConfig(e *env) (*config, error) {
	cfg := &config{path: configPath(e)}
	data, err := os.ReadFile(cfg.path)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return nil, err
	}
	d := yaml.NewDecoder(bytes.NewReader(data))
	d.KnownFields(true)
	if err := d.Decode(cfg); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: %w", cfg.path, err)
	}
	return cfg, nil
}

func (c *config) applyDefaults(o *options) {
	if c.Provider != "" {
		o.provider = c.Provider
	}
	if c.Jobs > 0 {
		o.jobs = c.Jobs
	}
	if c.Retries != nil {
		o.retries = *c.Retries
	}
	if c.Key != "" {
		o.key = c.Key
	}
}

func applyEnv(o *options, e *env) {
	if v := e.getenv("HUNCH_PROVIDER"); v != "" {
		o.provider = v
	}
	if v := e.getenv("HUNCH_MODEL"); v != "" {
		o.model = v
	}
	if n, err := strconv.Atoi(e.getenv("HUNCH_JOBS")); err == nil && n > 0 {
		o.jobs = n
	}
	if n, err := strconv.Atoi(e.getenv("HUNCH_RETRIES")); err == nil && n >= 0 {
		o.retries = n
	}
}

// saveProvider records the chosen provider and model in the config file. It
// edits the YAML tree in place so other settings and comments survive.
func (c *config) saveProvider(id, model string) error {
	var doc yaml.Node
	data, err := os.ReadFile(c.path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return err
	default:
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return fmt.Errorf("%s: %w", c.path, err)
		}
	}
	if doc.Kind == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return fmt.Errorf("%s: top level must be a mapping", c.path)
	}
	setScalar(root, "provider", id)
	setScalar(mappingAt(mappingAt(root, "providers"), id), "model", model)

	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(c.path, out.Bytes(), 0o644); err != nil {
		return err
	}
	c.Provider = id
	if c.Providers == nil {
		c.Providers = map[string]providerConfig{}
	}
	pc := c.Providers[id]
	pc.Model = model
	c.Providers[id] = pc
	return nil
}

// mappingAt returns the mapping under key, creating it if needed.
func mappingAt(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			v := m.Content[i+1]
			if v.Kind != yaml.MappingNode {
				*v = yaml.Node{Kind: yaml.MappingNode}
			}
			return v
		}
	}
	v := &yaml.Node{Kind: yaml.MappingNode}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, v)
	return v
}

func setScalar(m *yaml.Node, key, value string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1].Kind = yaml.ScalarNode
			m.Content[i+1].Tag = ""
			m.Content[i+1].Value = value
			m.Content[i+1].Content = nil
			return
		}
	}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Value: value})
}
