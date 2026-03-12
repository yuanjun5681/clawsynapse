package api

import "testing"

func TestNormalizeBaseURL(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		expects string
	}{
		{name: "default", input: "", expects: "http://127.0.0.1:18080"},
		{name: "host and port", input: "127.0.0.1:19090", expects: "http://127.0.0.1:19090"},
		{name: "trim slash", input: "http://127.0.0.1:18080/", expects: "http://127.0.0.1:18080"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeBaseURL(tt.input)
			if got != tt.expects {
				t.Fatalf("normalizeBaseURL(%q) = %q, want %q", tt.input, got, tt.expects)
			}
		})
	}
}
