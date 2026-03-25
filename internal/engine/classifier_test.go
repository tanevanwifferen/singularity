package engine

import (
	"testing"
)

func TestParseClassification(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantCat     PromptCategory
		wantModel   string
		wantEffort  string
		wantSummary string
		wantErr     bool
	}{
		{
			name:      "clean planning response",
			input:     `{"category": "planning", "reason": "user wants to think through architecture"}`,
			wantCat:   CategoryPlanning,
			wantModel: "opus",
		},
		{
			name:      "clean implementation response",
			input:     `{"category": "implementation", "reason": "user wants code changes"}`,
			wantCat:   CategoryImplementation,
			wantModel: "sonnet",
		},
		{
			name:      "json with surrounding text",
			input:     "Here is my classification:\n{\"category\": \"planning\", \"reason\": \"design question\"}\n",
			wantCat:   CategoryPlanning,
			wantModel: "opus",
		},
		{
			name:      "uppercase category",
			input:     `{"category": "PLANNING", "reason": "architecture discussion"}`,
			wantCat:   CategoryPlanning,
			wantModel: "opus",
		},
		{
			name:      "unknown category defaults to sonnet",
			input:     `{"category": "other", "reason": "unclear"}`,
			wantCat:   CategoryImplementation,
			wantModel: "sonnet",
		},
		{
			name:    "no json",
			input:   "this is just text with no json",
			wantErr: true,
		},
		{
			name:    "empty input",
			input:   "",
			wantErr: true,
		},
		{
			name:    "malformed json content",
			input:   "{not: valid}",
			wantErr: true,
		},
		{
			name:       "effort field high",
			input:      `{"category": "implementation", "effort": "high", "reason": "complex task", "summary": "refactor auth"}`,
			wantCat:    CategoryImplementation,
			wantModel:  "sonnet",
			wantEffort: "high",
		},
		{
			name:       "invalid effort defaults to medium",
			input:      `{"category": "planning", "effort": "extreme", "reason": "design", "summary": "plan infra"}`,
			wantCat:    CategoryPlanning,
			wantModel:  "opus",
			wantEffort: "medium",
		},
		{
			name:        "summary field populated",
			input:       `{"category": "implementation", "effort": "low", "reason": "tiny fix", "summary": "fix typo in README"}`,
			wantCat:     CategoryImplementation,
			wantModel:   "sonnet",
			wantEffort:  "low",
			wantSummary: "fix typo in README",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseClassification(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Category != tt.wantCat {
				t.Errorf("category: got %q, want %q", result.Category, tt.wantCat)
			}
			if result.Model != tt.wantModel {
				t.Errorf("model: got %q, want %q", result.Model, tt.wantModel)
			}
			if tt.wantEffort != "" && result.Effort != tt.wantEffort {
				t.Errorf("effort: got %q, want %q", result.Effort, tt.wantEffort)
			}
			if tt.wantSummary != "" && result.Summary != tt.wantSummary {
				t.Errorf("summary: got %q, want %q", result.Summary, tt.wantSummary)
			}
		})
	}
}
