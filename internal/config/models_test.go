package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultModelsConfigResolve(t *testing.T) {
	models := DefaultModelsConfig()

	tests := []struct {
		name    string
		backend string
		model   string
		want    string
	}{
		{"pi sonnet", "pi", "sonnet", "anthropic/claude-sonnet-5"},
		{"pi opus", "pi", "opus", "anthropic/claude-opus-5"},
		{"pi haiku", "pi", "haiku", "anthropic/claude-haiku-4-5"},
		{"pi uppercase alias", "pi", "OPUS", "anthropic/claude-opus-5"},
		{"claude takes short names", "claude", "sonnet", "sonnet"},
		{"qualified id passes through", "pi", "anthropic/claude-opus-4-8", "anthropic/claude-opus-4-8"},
		{"unknown short name passes through", "pi", "sonnet-4", "sonnet-4"},
		{"unknown backend passes through", "codex", "sonnet", "sonnet"},
		{"empty model", "pi", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := models.ResolveModel(tt.backend, tt.model); got != tt.want {
				t.Errorf("ResolveModel(%q, %q) = %q, want %q", tt.backend, tt.model, got, tt.want)
			}
		})
	}
}

func TestNilModelsConfigFallsBackToDefaults(t *testing.T) {
	var models *ModelsConfig
	if got := models.ResolveModel("pi", "sonnet"); got != "anthropic/claude-sonnet-5" {
		t.Errorf("ResolveModel on nil table = %q, want the compiled-in default", got)
	}
	if got := models.ClassifierModel("pi"); got != "anthropic/claude-haiku-4-5-20251001" {
		t.Errorf("ClassifierModel on nil table = %q, want the compiled-in default", got)
	}
}

func TestClassifierModel(t *testing.T) {
	tests := []struct {
		name    string
		models  *ModelsConfig
		backend string
		want    string
	}{
		{"pi default", DefaultModelsConfig(), "pi", "anthropic/claude-haiku-4-5-20251001"},
		{"claude default", DefaultModelsConfig(), "claude", "haiku"},
		{"unknown backend", DefaultModelsConfig(), "codex", ""},
		{
			name: "configured override",
			models: &ModelsConfig{Backends: map[string]BackendModels{
				"pi": {ClassifierModel: "openai/gpt-5-mini"},
			}},
			backend: "pi",
			want:    "openai/gpt-5-mini",
		},
		{
			name: "empty override falls back",
			models: &ModelsConfig{Backends: map[string]BackendModels{
				"pi": {ClassifierModel: ""},
			}},
			backend: "pi",
			want:    "anthropic/claude-haiku-4-5-20251001",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.models.ClassifierModel(tt.backend); got != tt.want {
				t.Errorf("ClassifierModel(%q) = %q, want %q", tt.backend, got, tt.want)
			}
		})
	}
}

func TestLoadModelsConfig(t *testing.T) {
	tests := []struct {
		name string
		// content is written to models.json; nil means "no file at all"
		content   []byte
		wantErr   bool
		wantPi    map[string]string // alias -> expected resolution
		wantClass string
	}{
		{
			name:      "missing file yields defaults",
			content:   nil,
			wantErr:   true,
			wantPi:    map[string]string{"sonnet": "anthropic/claude-sonnet-5"},
			wantClass: "anthropic/claude-haiku-4-5-20251001",
		},
		{
			name:      "unparsable file yields defaults",
			content:   []byte("{ not json"),
			wantErr:   true,
			wantPi:    map[string]string{"opus": "anthropic/claude-opus-5"},
			wantClass: "anthropic/claude-haiku-4-5-20251001",
		},
		{
			name:      "user overrides win",
			content:   []byte(`{"version":1,"backends":{"pi":{"classifier_model":"openai/gpt-5-mini","aliases":{"sonnet":"openai/gpt-5"}}}}`),
			wantPi:    map[string]string{"sonnet": "openai/gpt-5"},
			wantClass: "openai/gpt-5-mini",
		},
		{
			name:      "omitted aliases fall back to defaults",
			content:   []byte(`{"version":1,"backends":{"pi":{"aliases":{"sonnet":"openai/gpt-5"}}}}`),
			wantPi:    map[string]string{"sonnet": "openai/gpt-5", "opus": "anthropic/claude-opus-5"},
			wantClass: "anthropic/claude-haiku-4-5-20251001",
		},
		{
			name:      "empty object falls back entirely",
			content:   []byte(`{}`),
			wantPi:    map[string]string{"sonnet": "anthropic/claude-sonnet-5"},
			wantClass: "anthropic/claude-haiku-4-5-20251001",
		},
		{
			name:      "new backend entries are kept",
			content:   []byte(`{"backends":{"codex":{"classifier_model":"openai/gpt-5-mini","aliases":{"fast":"openai/gpt-5-mini"}}}}`),
			wantPi:    map[string]string{"sonnet": "anthropic/claude-sonnet-5"},
			wantClass: "anthropic/claude-haiku-4-5-20251001",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ModelsFileName)
			if tt.content != nil {
				if err := os.WriteFile(path, tt.content, 0644); err != nil {
					t.Fatal(err)
				}
			}

			models, err := LoadModelsConfig(path)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if models == nil {
				t.Fatal("LoadModelsConfig returned a nil table")
			}
			for alias, want := range tt.wantPi {
				if got := models.ResolveModel("pi", alias); got != want {
					t.Errorf("ResolveModel(pi, %q) = %q, want %q", alias, got, want)
				}
			}
			if got := models.ClassifierModel("pi"); got != tt.wantClass {
				t.Errorf("ClassifierModel(pi) = %q, want %q", got, tt.wantClass)
			}
		})
	}
}

func TestLoadModelsConfigKeepsUnknownBackends(t *testing.T) {
	path := filepath.Join(t.TempDir(), ModelsFileName)
	content := `{"backends":{"codex":{"classifier_model":"openai/gpt-5-mini","aliases":{"fast":"openai/gpt-5-mini"}}}}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	models, err := LoadModelsConfig(path)
	if err != nil {
		t.Fatalf("LoadModelsConfig: %v", err)
	}
	if got := models.ResolveModel("codex", "fast"); got != "openai/gpt-5-mini" {
		t.Errorf("ResolveModel(codex, fast) = %q, want openai/gpt-5-mini", got)
	}
	if got := models.ClassifierModel("codex"); got != "openai/gpt-5-mini" {
		t.Errorf("ClassifierModel(codex) = %q, want openai/gpt-5-mini", got)
	}
}

func TestSaveModelsConfigRoundTrip(t *testing.T) {
	// Nested dir: SaveModelsConfig must create it.
	path := filepath.Join(t.TempDir(), "singularity", ModelsFileName)

	if err := SaveModelsConfig(path, DefaultModelsConfig()); err != nil {
		t.Fatalf("SaveModelsConfig: %v", err)
	}

	models, err := LoadModelsConfig(path)
	if err != nil {
		t.Fatalf("LoadModelsConfig: %v", err)
	}
	for backend, want := range map[string]string{
		"pi":     "anthropic/claude-opus-5",
		"claude": "opus",
	} {
		if got := models.ResolveModel(backend, "opus"); got != want {
			t.Errorf("ResolveModel(%q, opus) = %q, want %q", backend, got, want)
		}
	}
}

func TestGetModelsPathSitsNextToConfig(t *testing.T) {
	if got, want := filepath.Dir(GetModelsPath()), filepath.Dir(GetConfigPath()); got != want {
		t.Errorf("models dir = %q, want %q", got, want)
	}
	if got := filepath.Base(GetModelsPath()); got != ModelsFileName {
		t.Errorf("models file = %q, want %q", got, ModelsFileName)
	}
}
