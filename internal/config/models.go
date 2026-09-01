package config

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// ModelsFileName is the name of the model table file, stored next to config.json.
const ModelsFileName = "models.json"

// BackendModels is the model table for one agent backend.
type BackendModels struct {
	// ClassifierModel is the model the smart router uses for its one-shot
	// classification call. Separate from Aliases because it is the entry
	// most users will want to change.
	ClassifierModel string `json:"classifier_model"`
	// Aliases maps a short name ("sonnet") to a model id the backend accepts.
	Aliases map[string]string `json:"aliases"`
}

// ModelsConfig is the on-disk model table (~/.config/singularity/models.json).
// It is keyed by backend name so adding a provider is a file edit, not a code
// change. Model ids that are already fully qualified are never rewritten.
type ModelsConfig struct {
	Version  int                      `json:"version"`
	Backends map[string]BackendModels `json:"backends"`
}

// DefaultModelsConfig returns the compiled-in fallback model table.
//
// The pi ids were verified against `pi --list-models` (pi model registry, the
// anthropic provider): claude-haiku-4-5, claude-sonnet-4-5 and claude-opus-4-5
// all resolve to catalogued models. The claude backend takes the bare short
// names directly.
func DefaultModelsConfig() *ModelsConfig {
	return &ModelsConfig{
		Version: 1,
		Backends: map[string]BackendModels{
			"pi": {
				ClassifierModel: "anthropic/claude-haiku-4-5",
				Aliases: map[string]string{
					"haiku":  "anthropic/claude-haiku-4-5",
					"sonnet": "anthropic/claude-sonnet-4-5",
					"opus":   "anthropic/claude-opus-4-5",
				},
			},
			"claude": {
				ClassifierModel: "haiku",
				Aliases: map[string]string{
					"haiku":  "haiku",
					"sonnet": "sonnet",
					"opus":   "opus",
				},
			},
		},
	}
}

// GetModelsPath returns the default model table path, alongside config.json.
func GetModelsPath() string {
	return filepath.Join(filepath.Dir(GetConfigPath()), ModelsFileName)
}

// LoadModelsConfig reads the model table from path. A missing or unparsable
// file is not fatal: the compiled-in defaults are returned together with the
// error so the caller can log it (and create the file when it is absent).
// Backends and aliases the file omits are filled in from the defaults.
func LoadModelsConfig(path string) (*ModelsConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return DefaultModelsConfig(), err
	}

	var models ModelsConfig
	if err := json.Unmarshal(data, &models); err != nil {
		return DefaultModelsConfig(), fmt.Errorf("parse %s: %w", path, err)
	}

	models.ApplyDefaults()
	return &models, nil
}

// ApplyDefaults fills anything the table omits with the compiled-in value so a
// partial table stays usable. Safe to call on a nil receiver.
func (m *ModelsConfig) ApplyDefaults() {
	if m == nil {
		return
	}
	def := DefaultModelsConfig()
	if m.Version == 0 {
		m.Version = def.Version
	}
	if m.Backends == nil {
		m.Backends = map[string]BackendModels{}
	}
	for name, fallback := range def.Backends {
		backend, ok := m.Backends[name]
		if !ok {
			m.Backends[name] = fallback
			continue
		}
		if backend.ClassifierModel == "" {
			backend.ClassifierModel = fallback.ClassifierModel
		}
		if backend.Aliases == nil {
			backend.Aliases = map[string]string{}
		}
		for short, id := range fallback.Aliases {
			if _, ok := backend.Aliases[short]; !ok {
				backend.Aliases[short] = id
			}
		}
		m.Backends[name] = backend
	}
}

// SaveModelsConfig writes the model table to path, creating the directory.
func SaveModelsConfig(path string, models *ModelsConfig) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}
	data, err := json.MarshalIndent(models, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal model table: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0644); err != nil {
		return fmt.Errorf("failed to write model table: %w", err)
	}
	return nil
}

// LoadDefaultModelsConfig loads the model table from its default path, writing
// the file with the current defaults when it does not exist so a user can find
// and edit it. Never fails: problems are logged and the defaults are used.
func LoadDefaultModelsConfig() *ModelsConfig {
	path := GetModelsPath()
	models, err := LoadModelsConfig(path)
	switch {
	case err == nil:
		return models
	case os.IsNotExist(err):
		if werr := SaveModelsConfig(path, models); werr != nil {
			log.Printf("models: could not create %s: %v (using built-in defaults)", path, werr)
		} else {
			log.Printf("models: wrote default model table to %s", path)
		}
	default:
		log.Printf("models: using built-in defaults: %v", err)
	}
	return models
}

// ResolveModel maps a short model name to the id configured for backend.
// Ids that are already fully qualified ("provider/model") and short names with
// no alias are returned unchanged, so an unknown name reaches the CLI as typed
// rather than silently becoming a different model.
func (m *ModelsConfig) ResolveModel(backend, model string) string {
	if model == "" || strings.Contains(model, "/") {
		return model
	}
	if m == nil {
		m = DefaultModelsConfig()
	}
	if b, ok := m.Backends[backend]; ok {
		if id, ok := b.Aliases[strings.ToLower(model)]; ok {
			return id
		}
	}
	return model
}

// ClassifierModel returns the smart-router classification model for backend,
// falling back to the compiled-in default when unset or unknown.
func (m *ModelsConfig) ClassifierModel(backend string) string {
	if m != nil {
		if b, ok := m.Backends[backend]; ok && b.ClassifierModel != "" {
			return b.ClassifierModel
		}
	}
	if b, ok := DefaultModelsConfig().Backends[backend]; ok {
		return b.ClassifierModel
	}
	return ""
}
