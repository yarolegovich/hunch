package provider

import "testing"

func TestParseModels(t *testing.T) {
	gw := []byte(`{"data":[{"id":"~typesafe/jev-latest","description":"Jev","created":1760400000},{"id":"openai/gpt-6"}]}`)
	ms, err := parseModels(gw, OpenRouter.filter)
	if err != nil || len(ms) != 1 || ms[0].Name != "~typesafe/jev-latest" || ms[0].ReleaseDate != "2025-10-14" {
		t.Errorf("%v %+v", err, ms)
	}
	ms, err = parseModels([]byte(`{"data":[{"id":"jev-1.13"},{"id":"jev-1.13-free"},{"id":"glm-5"}]}`), Zen.filter)
	if err != nil || len(ms) != 2 {
		t.Errorf("%v %+v", err, ms)
	}
	ms, err = parseModels([]byte(`{"models":[{"name":"jev-latest","release_date":"2026-10-01"}]}`), nil)
	if err != nil || len(ms) != 1 || ms[0].ReleaseDate != "2026-10-01" {
		t.Errorf("%v %+v", err, ms)
	}
}

func TestRegistry(t *testing.T) {
	for _, p := range All() {
		if p.ID == "" || p.Name == "" || p.BaseURL == "" || p.KeyEnv == "" || p.KeyURL == "" || len(p.Models) == 0 {
			t.Errorf("incomplete provider %+v", p)
		}
		if got, err := Lookup(p.ID); err != nil || got != p {
			t.Errorf("lookup %s: %v", p.ID, err)
		}
	}
	if _, err := Lookup("nope"); err == nil {
		t.Error("unknown provider accepted")
	}
}
