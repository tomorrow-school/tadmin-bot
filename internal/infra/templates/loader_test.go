package templates

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"admin-bot/internal/domain"
)

// writeTemplate writes a .txt file with the given content into a fresh dir
// and returns the dir path. The dir is cleaned up automatically.
func writeTemplate(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name+".txt"), []byte(content), 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}
	return dir
}

func TestRender_SimpleSubstitution(t *testing.T) {
	dir := writeTemplate(t, "greet", "Hello {{NAME}}, your team has {{COUNT}} members.")
	l := NewFileLoader(dir)

	got, err := l.Render("greet", map[string]string{
		"NAME":  "Alice",
		"COUNT": "5",
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := "Hello Alice, your team has 5 members."
	if got != want {
		t.Errorf("Render = %q, want %q", got, want)
	}
}

func TestRender_MissingVarLeftAsLiteral(t *testing.T) {
	dir := writeTemplate(t, "x", "Set: {{A}}, unset: {{B}}")
	l := NewFileLoader(dir)

	got, err := l.Render("x", map[string]string{"A": "ok"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	// Unsupplied vars stay as literals — important because templates use
	// them as a fall-back indicator when no value is available.
	want := "Set: ok, unset: {{B}}"
	if got != want {
		t.Errorf("Render = %q, want %q", got, want)
	}
}

func TestRender_RepeatedPlaceholder(t *testing.T) {
	dir := writeTemplate(t, "rep", "{{X}}-{{X}}-{{X}}")
	l := NewFileLoader(dir)

	got, err := l.Render("rep", map[string]string{"X": "1"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "1-1-1" {
		t.Errorf("Render = %q, want %q", got, "1-1-1")
	}
}

func TestRender_EmptyValue(t *testing.T) {
	dir := writeTemplate(t, "e", "before|{{X}}|after")
	l := NewFileLoader(dir)

	got, err := l.Render("e", map[string]string{"X": ""})
	if err != nil {
		t.Fatal(err)
	}
	if got != "before||after" {
		t.Errorf("Render = %q, want %q", got, "before||after")
	}
}

func TestRender_NilVars(t *testing.T) {
	dir := writeTemplate(t, "nilv", "plain {{X}} text")
	l := NewFileLoader(dir)

	got, err := l.Render("nilv", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "plain {{X}} text" {
		t.Errorf("Render with nil vars = %q, want literal placeholder", got)
	}
}

func TestRender_MissingFileReturnsErrTemplateNotFound(t *testing.T) {
	l := NewFileLoader(t.TempDir())

	_, err := l.Render("does_not_exist", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, domain.ErrTemplateNotFound) {
		t.Errorf("expected ErrTemplateNotFound, got %v", err)
	}
	if !strings.Contains(err.Error(), "does_not_exist") {
		t.Errorf("error should mention the missing key, got %v", err)
	}
}

func TestRender_ValueWithBracesIsNotRecursivelyExpanded(t *testing.T) {
	// If a substitution value happens to contain "{{...}}", it must not be
	// expanded a second time. Otherwise users could inject placeholders.
	dir := writeTemplate(t, "rec", "A={{A}} B={{B}}")
	l := NewFileLoader(dir)

	got, err := l.Render("rec", map[string]string{
		"A": "{{B}}",
		"B": "real-B",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Map iteration order is non-deterministic, so the test passes if EITHER
	// safe outcome is observed. The point is that {{B}} from the value must
	// not produce "real-B" via a second pass.
	// Acceptable: "A={{B}} B=real-B" (A processed first) OR
	//             "A={{B}} B=real-B" (A processed after B, since strings.ReplaceAll
	//             on "{{A}}" doesn't change the value of A's substitution).
	// The unacceptable outcome is "A=real-B B=real-B".
	if strings.Contains(got, "A=real-B") {
		t.Errorf("recursive expansion detected: %q", got)
	}
}

func TestRender_PreservesNewlines(t *testing.T) {
	dir := writeTemplate(t, "multi", "line1\n{{X}}\nline3")
	l := NewFileLoader(dir)
	got, err := l.Render("multi", map[string]string{"X": "Y"})
	if err != nil {
		t.Fatal(err)
	}
	want := "line1\nY\nline3"
	if got != want {
		t.Errorf("Render = %q, want %q", got, want)
	}
}

// TestAnnouncementTemplatesExist guards the /announce menu: every ready-made
// announcement must have a file in messages/, otherwise the admin picks a
// button and gets "шаблон не найден" instead of a text.
func TestAnnouncementTemplatesExist(t *testing.T) {
	loader := NewFileLoader(filepath.Join("..", "..", "..", "messages"))

	// Piscine Go offers every announcement, so its list is the full catalogue.
	for _, kind := range domain.AnnouncementKindsFor(domain.PiscineGo) {
		text, err := loader.Render(string(kind.Message), map[string]string{
			"RAID_NAME": "quad", "TEAMS_COUNT": "18", "SHEET_URL": "https://sheet",
		})
		if err != nil {
			t.Errorf("announcement %q (%s): %v", kind.ID, kind.Message, err)
			continue
		}
		if strings.TrimSpace(text) == "" {
			t.Errorf("announcement %q (%s) renders empty", kind.ID, kind.Message)
		}
		if strings.Contains(text, "{{") {
			t.Errorf("announcement %q (%s) has unresolved placeholders:\n%s", kind.ID, kind.Message, text)
		}
	}
}
