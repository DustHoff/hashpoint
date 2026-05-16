package feedback

import (
	"strings"
	"testing"
)

func TestRender_FullPayload(t *testing.T) {
	in := Input{
		Title:       "Tracker hängt nach Idle",
		Category:    CategoryBug,
		Severity:    SeverityHigh,
		Description: "Nach 10 Minuten Idle reagiert nichts mehr.",
		Expected:    "Tracker setzt wieder ein.",
		Actual:      "Bleibt stehen.",
		Repro:       "1. Sperren\n2. Warten\n3. Entsperren",
		About:       AboutInfo{Version: "1.2.3", Commit: "abcdef0", BuildDate: "2026-05-16"},
		LogTail:     []byte(`{"level":"WARN","msg":"x"}` + "\n"),
		LogWindow:   LogWindowDay,
	}
	got := Render(in)
	mustContainAll(t, got,
		"**Kategorie:** Bug",
		"**Schweregrad:** Hoch",
		"### Beschreibung\nNach 10 Minuten Idle reagiert nichts mehr.",
		"### Erwartetes Verhalten\nTracker setzt wieder ein.",
		"### Tatsächliches Verhalten\nBleibt stehen.",
		"### Schritte zur Reproduktion\n1. Sperren\n2. Warten\n3. Entsperren",
		"### Über",
		"- **Version:** 1.2.3",
		"- **Commit:** abcdef0",
		"- **Build:** 2026-05-16",
		"<details>",
		"letzte 24 Stunden",
		"```\n"+`{"level":"WARN","msg":"x"}`+"\n```",
		"</details>",
	)
}

func TestRender_SkipsEmptySectionsAndOmitsLogBlock(t *testing.T) {
	in := Input{
		Category:    CategoryFeature,
		Severity:    SeverityLow,
		Description: "kurze Idee",
		About:       AboutInfo{Version: "dev"},
	}
	got := Render(in)
	mustContainAll(t, got,
		"**Kategorie:** Feature-Wunsch",
		"**Schweregrad:** Niedrig",
		"### Beschreibung\nkurze Idee",
		"- **Version:** dev",
		"- **Commit:** —",
		"- **Build:** —",
	)
	for _, forbidden := range []string{
		"### Erwartetes Verhalten",
		"### Tatsächliches Verhalten",
		"### Schritte zur Reproduktion",
		"<details>",
	} {
		if strings.Contains(got, forbidden) {
			t.Errorf("expected %q to be absent, got:\n%s", forbidden, got)
		}
	}
}

func TestLabelsFor_TagsUserFeedback(t *testing.T) {
	labels := LabelsFor(Input{Category: CategoryQuestion, Severity: SeverityCritical})
	want := []string{"question", "severity:critical", "user-feedback"}
	if len(labels) != len(want) {
		t.Fatalf("labels=%v want %v", labels, want)
	}
	for i, w := range want {
		if labels[i] != w {
			t.Errorf("labels[%d]=%q want %q", i, labels[i], w)
		}
	}
}

func TestCategoryAndSeverityLabel_Fallbacks(t *testing.T) {
	if got := CategoryLabel(Category("nonsense")); got != "bug" {
		t.Errorf("CategoryLabel fallback=%q want bug", got)
	}
	if got := SeverityLabel(Severity("urgent")); got != "severity:medium" {
		t.Errorf("SeverityLabel fallback=%q want severity:medium", got)
	}
}

func mustContainAll(t *testing.T, got string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if !strings.Contains(got, p) {
			t.Errorf("rendered body missing %q. Full:\n%s", p, got)
		}
	}
}
