package home

import (
	"context"
	"strings"
	"testing"
)

func renderIndex(t *testing.T) string {
	t.Helper()
	var out strings.Builder
	if err := Index().Render(context.Background(), &out); err != nil {
		t.Fatalf("render index: %v", err)
	}
	return out.String()
}

func TestIndexRendersCompletePage(t *testing.T) {
	html := renderIndex(t)

	for _, marker := range []string{
		"<!doctype html>",
		"<title>Airway — The full-stack Go web framework</title>",
		`class="home-page"`,
		`id="main"`,
		`id="features"`,
		`id="get-started"`,
		`class="home-footer`,
	} {
		if !strings.Contains(html, marker) {
			t.Fatalf("index output missing %q", marker)
		}
	}
}

func TestIndexLinksToRepository(t *testing.T) {
	html := renderIndex(t)
	if !strings.Contains(html, `href="`+repositoryURL) {
		t.Fatal("index does not link to the repository")
	}
	if !strings.Contains(html, repositoryURL+"#readme") {
		t.Fatal("index does not link to the documentation anchor")
	}
}

func TestIndexRendersAllFeatureCards(t *testing.T) {
	html := renderIndex(t)
	if count := strings.Count(html, `class="feature-card"`); count != 6 {
		t.Fatalf("feature cards = %d, want 6", count)
	}
	for _, title := range []string{
		"A data layer that speaks Go",
		"HTML, with a Go mindset",
		"A head start, built in",
		"Files at home. Or in the cloud.",
		"Ready for real time",
		"A straightforward way to ship",
	} {
		if !strings.Contains(html, title) {
			t.Fatalf("index output missing feature %q", title)
		}
	}
}

func TestIndexShowsQuickStartCommands(t *testing.T) {
	html := renderIndex(t)
	for _, marker := range []string{"cp .env.example .env", "AIRWAY_ENV=local go run .", "localhost:1905"} {
		if !strings.Contains(html, marker) {
			t.Fatalf("index quick start missing %q", marker)
		}
	}
}
