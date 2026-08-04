package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// GlobalConfig is the user-wide config at <KgaiHome>/config.json. It holds defaults
// that apply to every project on this machine; a project's own kg.config.json always
// wins. Distinct from the per-store kg.config.json: nothing identity-bound (installId,
// tokens) belongs here.
type GlobalConfig struct {
	// Remote is the default sync remote for projects that have none configured
	// locally. The literal "{project}" expands to the project directory's name, so
	// s3://bucket/kg/{project} gives every project its own prefix; without the
	// placeholder all projects share one remote (and therefore one merged graph).
	Remote string `json:"remote,omitempty"`
}

// RemoteNone is the per-project sentinel that opts a project out of syncing even when
// a global remote is configured ("this project stays local").
const RemoteNone = "none"

func globalConfigPath() string { return filepath.Join(KgaiHome(), "config.json") }

// LoadGlobalConfig reads <KgaiHome>/config.json. A missing file is an empty config.
func LoadGlobalConfig() (GlobalConfig, error) {
	var gc GlobalConfig
	b, err := os.ReadFile(globalConfigPath())
	if os.IsNotExist(err) {
		return gc, nil
	}
	if err != nil {
		return gc, err
	}
	if err := json.Unmarshal(b, &gc); err != nil {
		return gc, fmt.Errorf("corrupt %s: %w", globalConfigPath(), err)
	}
	return gc, nil
}

// SaveGlobalConfig writes <KgaiHome>/config.json, creating the home dir if needed.
func SaveGlobalConfig(gc GlobalConfig) error {
	if err := os.MkdirAll(KgaiHome(), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(gc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(globalConfigPath(), append(b, '\n'), 0o644)
}

// EffectiveRemote resolves the sync remote this store actually uses, and where it came
// from: "local" (this store's kg.config.json), "global" (<KgaiHome>/config.json),
// "disabled" (local remote is "none" — opted out), or "" when nothing is configured.
func (s *Store) EffectiveRemote() (url, source string) {
	switch local := s.Config.Remote; {
	case local == RemoteNone:
		return "", "disabled"
	case local != "":
		return local, "local"
	}
	gc, err := LoadGlobalConfig()
	if err != nil || gc.Remote == "" {
		return "", ""
	}
	return ExpandRemote(gc.Remote), "global"
}

// ExpandRemote fills the {project} placeholder with the project directory's name.
func ExpandRemote(remote string) string {
	return strings.ReplaceAll(remote, "{project}", filepath.Base(ProjectRoot()))
}
