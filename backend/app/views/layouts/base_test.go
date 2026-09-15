package layouts

import (
	"context"
	"strings"
	"testing"

	"github.com/a-h/templ"
)

func render(t *testing.T, component templ.Component) string {
	t.Helper()
	var out strings.Builder
	if err := component.Render(context.Background(), &out); err != nil {
		t.Fatalf("render: %v", err)
	}
	return out.String()
}

func TestBaseRendersShellWithTitleAndDescription(t *testing.T) {
	html := render(t, Base("Test Title", "A test page."))

	for _, marker := range []string{
		"<!doctype html>",
		"<title>Test Title</title>",
		`<meta name="description" content="A test page.">`,
		`<meta property="og:title" content="Test Title">`,
		`<meta property="og:type" content="website">`,
	} {
		if !strings.Contains(html, marker) {
			t.Fatalf("Base output missing %q", marker)
		}
	}
}

func TestBaseOmitsOptionalMetaWithoutDescription(t *testing.T) {
	html := render(t, Base("Bare Title"))

	if !strings.Contains(html, "<title>Bare Title</title>") {
		t.Fatal("Base output missing title")
	}
	for _, absent := range []string{`name="description"`, `property="og:title"`} {
		if strings.Contains(html, absent) {
			t.Fatalf("Base without description must not render %s", absent)
		}
	}
}

func TestBaseRendersChildren(t *testing.T) {
	var out strings.Builder
	ctx := templ.WithChildren(context.Background(), templ.Raw("<p>body-content</p>"))
	if err := Base("With Body").Render(ctx, &out); err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(out.String(), "<p>body-content</p>") {
		t.Fatalf("Base output missing children: %s", out.String())
	}
}
