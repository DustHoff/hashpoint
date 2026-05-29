package feedback

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dusthoff/hashpoint/internal/crashguard"
)

// Category names a high-level issue type. The frontend dropdown maps
// to these constants; the package translates each to a GitHub label
// when CategoryLabel is called.
type Category string

// Severity names how disruptive the reported behaviour is. Maps 1:1
// to severity:<name> labels in the repo.
type Severity string

// Category constants — the trailing labels are the GitHub default
// labels for new repos, which keeps the label map intuitive even for
// users who poke at the repo directly.
const (
	CategoryBug      Category = "bug"
	CategoryFeature  Category = "feature"
	CategoryQuestion Category = "question"
)

// Severity constants.
const (
	SeverityLow      Severity = "low"
	SeverityMedium   Severity = "medium"
	SeverityHigh     Severity = "high"
	SeverityCritical Severity = "critical"
)

// AboutInfo mirrors what the "Über" tab surfaces, plus the data
// directory hint shown there. The body builder embeds these
// verbatim so the user can copy/paste them out of the rendered
// issue without ambiguity.
type AboutInfo struct {
	Version   string
	Commit    string
	BuildDate string
}

// Input is the structured payload the frontend submits. Optional
// fields use empty strings; the body builder skips sections that have
// no content so the rendered issue stays tidy.
type Input struct {
	Title       string
	Category    Category
	Severity    Severity
	Description string
	Expected    string
	Actual      string
	Repro       string
	About       AboutInfo
	// LogTail is the sanitized log slice (see ReadLogTail). Empty
	// when the user opted out of attaching the log.
	LogTail []byte
	// LogWindow is the user's choice — surfaced in the <details>
	// summary so reviewers know what range to expect. Ignored when
	// LogTail is empty.
	LogWindow LogWindow
	// Crashes lists abnormal-termination records detected in the log
	// (panics, unclean shutdowns, UI crashes). Rendered as their own
	// section so they survive log-tail truncation. Empty when none were
	// found or the user did not attach the log.
	Crashes []CrashRecord
}

// CategoryLabel maps a category to its GitHub label name. Falls back
// to "bug" for an unrecognised category — the form validator rejects
// those upstream, but defending here keeps the rendered issue valid
// even if the validation is bypassed.
func CategoryLabel(c Category) string {
	switch c {
	case CategoryFeature:
		return "enhancement"
	case CategoryQuestion:
		return "question"
	default:
		return "bug"
	}
}

// SeverityLabel maps a severity to its GitHub label name. Falls back
// to severity:medium for an unrecognised value.
func SeverityLabel(s Severity) string {
	switch s {
	case SeverityLow:
		return "severity:low"
	case SeverityHigh:
		return "severity:high"
	case SeverityCritical:
		return "severity:critical"
	default:
		return "severity:medium"
	}
}

// LabelsFor returns the ordered set of labels for an Input. The
// "user-feedback" tag is always attached so triage can distinguish
// in-app submissions from issues filed via the GitHub web UI.
func LabelsFor(in Input) []string {
	return []string{
		CategoryLabel(in.Category),
		SeverityLabel(in.Severity),
		"user-feedback",
	}
}

// Render builds the Markdown issue body. Sections with empty content
// are omitted; the About block is always present.
func Render(in Input) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**Kategorie:** %s\n", categoryLabel(in.Category))
	fmt.Fprintf(&b, "**Schweregrad:** %s\n\n", severityLabel(in.Severity))

	writeSection(&b, "Beschreibung", in.Description)
	writeSection(&b, "Erwartetes Verhalten", in.Expected)
	writeSection(&b, "Tatsächliches Verhalten", in.Actual)
	writeSection(&b, "Schritte zur Reproduktion", in.Repro)

	b.WriteString("---\n\n")
	b.WriteString("### Über\n")
	fmt.Fprintf(&b, "- **Version:** %s\n", fallbackDash(in.About.Version))
	fmt.Fprintf(&b, "- **Commit:** %s\n", fallbackDash(in.About.Commit))
	fmt.Fprintf(&b, "- **Build:** %s\n", fallbackDash(in.About.BuildDate))

	writeCrashes(&b, in.Crashes)

	if len(in.LogTail) > 0 {
		b.WriteString("\n<details>\n")
		fmt.Fprintf(&b, "<summary>Anwendungslog — %s (gekürzt; nur freigegebene Felder — Debug-Zeilen, Fenstertitel, Pfade und Personio-Daten wie Tenant/Mitarbeiter-ID werden entfernt)</summary>\n\n",
			windowLabel(in.LogWindow))
		b.WriteString("```\n")
		b.Write(in.LogTail)
		if in.LogTail[len(in.LogTail)-1] != '\n' {
			b.WriteByte('\n')
		}
		b.WriteString("```\n\n")
		b.WriteString("</details>\n")
	}
	return b.String()
}

// writeCrashes renders the "Erkannte Abstürze" section. No-op when the slice is
// empty, so a report without detected crashes is unchanged.
func writeCrashes(b *strings.Builder, crashes []CrashRecord) {
	if len(crashes) == 0 {
		return
	}
	fmt.Fprintf(b, "\n### Erkannte Abstürze (%d)\n", len(crashes))
	b.WriteString("_Automatisch aus dem Anwendungslog erfasst._\n\n")
	for _, c := range crashes {
		writeCrash(b, c)
	}
}

func writeCrash(b *strings.Builder, c CrashRecord) {
	when := "—"
	if !c.Time.IsZero() {
		when = c.Time.UTC().Format(time.RFC3339)
	}
	fmt.Fprintf(b, "- **%s** · %s", crashKindLabel(c.Event), when)
	if cause := strings.TrimSpace(c.Cause); cause != "" {
		fmt.Fprintf(b, " · %s", cause)
	}
	b.WriteByte('\n')
	// Remaining structured fields, sorted for a stable body.
	if len(c.Detail) > 0 {
		keys := make([]string, 0, len(c.Detail))
		for k := range c.Detail {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(b, "  - %s: %v\n", k, c.Detail[k])
		}
	}
	if stack := strings.TrimRight(c.Stack, "\n"); strings.TrimSpace(stack) != "" {
		b.WriteString("\n<details>\n<summary>Stacktrace</summary>\n\n```\n")
		b.WriteString(stack)
		b.WriteString("\n```\n\n</details>\n")
	}
}

// crashKindLabel maps a crash event value to a German section label.
func crashKindLabel(event string) string {
	switch event {
	case crashguard.EventPanic:
		return "Panic"
	case crashguard.EventUncleanShutdown:
		return "Unerwartetes Beenden"
	case crashguard.EventUICrash:
		return "UI-Absturz"
	default:
		return event
	}
}

func writeSection(b *strings.Builder, heading, body string) {
	body = strings.TrimSpace(body)
	if body == "" {
		return
	}
	fmt.Fprintf(b, "### %s\n%s\n\n", heading, body)
}

func fallbackDash(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "—"
	}
	return s
}

func categoryLabel(c Category) string {
	switch c {
	case CategoryFeature:
		return "Feature-Wunsch"
	case CategoryQuestion:
		return "Frage"
	default:
		return "Bug"
	}
}

func severityLabel(s Severity) string {
	switch s {
	case SeverityLow:
		return "Niedrig"
	case SeverityHigh:
		return "Hoch"
	case SeverityCritical:
		return "Kritisch"
	default:
		return "Mittel"
	}
}

func windowLabel(w LogWindow) string {
	switch w {
	case LogWindowHour:
		return "letzte Stunde"
	case LogWindowDay:
		return "letzte 24 Stunden"
	default:
		return "heute"
	}
}
