package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// DefaultPath returns the config file used when no --config is given.
func DefaultPath() string {
	return getDefaultConfigPath()
}

// Save writes cfg to path (DefaultPath when empty). Comments and keys the
// Config doesn't know about are kept; keys absent from the file are only
// added when their value differs from the default. The file is replaced
// atomically with 0600 permissions since it can hold proxy credentials.
func Save(path string, cfg *Config) error {
	if path == "" {
		path = getDefaultConfigPath()
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}

	var doc yaml.Node
	// #nosec G304 -- the user's own config file.
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if len(bytes.TrimSpace(data)) > 0 {
			if err := yaml.Unmarshal(data, &doc); err != nil {
				return fmt.Errorf("parse %s: %w", path, err)
			}
		}
	case !errors.Is(err, os.ErrNotExist):
		return err
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}
	root := doc.Content[0]

	values, err := configNode(cfg)
	if err != nil {
		return err
	}
	defaults, err := configNode(defaultConfig())
	if err != nil {
		return err
	}
	for i := 0; i+1 < len(values.Content); i += 2 {
		key, value := values.Content[i].Value, values.Content[i+1]
		if existing := mappingValue(root, key); existing != nil {
			value.LineComment = existing.LineComment
			*existing = *value
			continue
		}
		if sameScalar(value, mappingValue(defaults, key)) {
			continue
		}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
	}

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return err
	}
	return writeAtomic(path, out)
}

// configNode encodes c as a mapping node, rendering durations as "30s"
// rather than nanosecond integers.
func configNode(c *Config) (*yaml.Node, error) {
	var n yaml.Node
	if err := n.Encode(c); err != nil {
		return nil, err
	}
	for key, d := range map[string]time.Duration{"timeout": c.Timeout, "scrape_delay": c.ScrapeDelay} {
		if v := mappingValue(&n, key); v != nil {
			v.Tag, v.Value = "!!str", d.String()
		}
	}
	return &n, nil
}

func mappingValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func sameScalar(a, b *yaml.Node) bool {
	if a == nil || b == nil {
		return false
	}
	ab, errA := yaml.Marshal(a)
	bb, errB := yaml.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(ab, bb)
}

func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".scraper-*.yaml")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
