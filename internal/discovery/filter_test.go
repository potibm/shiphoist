package discovery

import (
	"context"
	"errors"
	"reflect"
	"regexp"
	"slices"
	"testing"

	"github.com/potibm/shiphoist/internal/core"
)

func mustCompile(t *testing.T, pattern string) *regexp.Regexp {
	t.Helper()

	compiled, err := regexp.Compile(pattern)
	if err != nil {
		t.Fatalf("compiling %q: %v", pattern, err)
	}

	return compiled
}

// exclude runs the real discoverer behind an ExcludeDiscoverer.
func exclude(t *testing.T, content, pattern string) ([]core.ImageUpdate, []core.Filtered) {
	t.Helper()

	discoverer := &ExcludeDiscoverer{
		Inner:   &ComposeDiscoverer{},
		Pattern: mustCompile(t, pattern),
	}

	updates, err := discoverer.Discover(context.Background(), writeTempFile(t, "docker-compose.yml", content))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	return updates, discoverer.Filtered()
}

func TestExcludeDiscoverer_DropsMatchingRepositories(t *testing.T) {
	content := `services:
  web:
    image: nginx:1.25.0
  mono:
    image: ghcr.io/acme/api:1.0
  other:
    image: ghcr.io/other/api:1.0
`

	updates, filtered := exclude(t, content, `^ghcr\.io/acme/`)

	if got := services(t, updates); !reflect.DeepEqual(got, []string{"web", "other"}) {
		t.Errorf("expected web and other, got %v", got)
	}

	if len(filtered) != 1 {
		t.Fatalf("expected 1 filtered reference, got %+v", filtered)
	}

	if filtered[0].Image != "ghcr.io/acme/api" {
		t.Errorf("expected the repository, got %q", filtered[0].Image)
	}

	if filtered[0].Reason != ReasonExcluded {
		t.Errorf("expected reason %q, got %q", ReasonExcluded, filtered[0].Reason)
	}
}

// The pattern is matched against the repository, never the tag, so `^nginx$`
// does what a user would expect rather than silently matching nothing.
func TestExcludeDiscoverer_MatchesRepositoryNotTag(t *testing.T) {
	content := `services:
  web:
    image: nginx:1.25.0
  pinned:
    image: nginx:1.26.0@sha256:abc
`

	updates, _ := exclude(t, content, `^nginx$`)

	if len(updates) != 0 {
		t.Errorf("expected both references to be excluded, got %v", services(t, updates))
	}
}

func TestExcludeDiscoverer_KeepsNonMatchingReferences(t *testing.T) {
	content := `services:
  web:
    image: nginx:1.25.0
`

	updates, filtered := exclude(t, content, `^ghcr\.io/acme/`)

	if got := services(t, updates); !reflect.DeepEqual(got, []string{"web"}) {
		t.Errorf("expected web to survive, got %v", got)
	}

	if len(filtered) != 0 {
		t.Errorf("expected nothing filtered, got %+v", filtered)
	}
}

func TestExcludeDiscoverer_NilPatternIsANoop(t *testing.T) {
	discoverer := &ExcludeDiscoverer{Inner: &ComposeDiscoverer{}}

	updates, err := discoverer.Discover(
		context.Background(),
		writeTempFile(t, "docker-compose.yml", "services:\n  web:\n    image: nginx:1.25.0\n"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(updates) != 1 {
		t.Errorf("expected the reference to survive, got %d", len(updates))
	}

	if got := discoverer.Filtered(); len(got) != 0 {
		t.Errorf("expected nothing filtered, got %+v", got)
	}
}

// A chain of filters has to account for everything it removed, or the report
// under-counts.
func TestExcludeDiscoverer_ReportsTheInnerFiltersToo(t *testing.T) {
	content := `services:
  a:
    image: nginx:1.25.0
  b:
    image: redis:7.2 # shiphoist-ignore
  c:
    image: ghcr.io/acme/api:1.0
  d:
    image: ghcr.io/acme/web:1.0
`

	discoverer := &ExcludeDiscoverer{
		Inner:   &ComposeDiscoverer{},
		Pattern: mustCompile(t, `^ghcr\.io/acme/`),
	}

	updates, err := discoverer.Discover(context.Background(), writeTempFile(t, "docker-compose.yml", content))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := services(t, updates); !reflect.DeepEqual(got, []string{"a"}) {
		t.Errorf("expected only a to survive, got %v", got)
	}

	filtered := discoverer.Filtered()
	if len(filtered) != 3 {
		t.Fatalf("expected 3 filtered references, got %+v", filtered)
	}

	// Reported in file order, whichever filter removed them.
	wantLines := []int{5, 7, 9}
	for i, want := range wantLines {
		if filtered[i].LineNumber != want {
			t.Errorf("position %d: expected line %d, got %d", i, want, filtered[i].LineNumber)
		}
	}

	reasons := []string{ReasonIgnoreDirective, ReasonExcluded, ReasonExcluded}
	if !slices.Equal([]string{filtered[0].Reason, filtered[1].Reason, filtered[2].Reason}, reasons) {
		t.Errorf("expected reasons %v, got %v", reasons, filtered)
	}
}

// An inner discoverer that does not report its filters must not break the chain.
func TestExcludeDiscoverer_UnfilteredInner(t *testing.T) {
	discoverer := &ExcludeDiscoverer{
		Inner:   fixedDiscoverer{updates: []core.ImageUpdate{{ImageName: "nginx", LineNumber: 4}}},
		Pattern: mustCompile(t, `^redis$`),
	}

	if _, err := discoverer.Discover(context.Background(), "docker-compose.yml"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := discoverer.Filtered(); len(got) != 0 {
		t.Errorf("expected nothing filtered, got %+v", got)
	}
}

func TestExcludeDiscoverer_PropagatesInnerErrors(t *testing.T) {
	discoverer := &ExcludeDiscoverer{
		Inner:   failingDiscoverer{},
		Pattern: mustCompile(t, `^nginx$`),
	}

	if _, err := discoverer.Discover(context.Background(), "docker-compose.yml"); err == nil {
		t.Fatal("expected the inner error to be propagated")
	}
}

// Filtering must not leak into the next run, as with the inline directive.
func TestExcludeDiscoverer_ExcludedResetsBetweenRuns(t *testing.T) {
	discoverer := &ExcludeDiscoverer{Inner: &ComposeDiscoverer{}, Pattern: mustCompile(t, `^nginx$`)}

	excluded := "services:\n  web:\n    image: nginx:1.25.0\n"
	kept := "services:\n  web:\n    image: redis:7.2\n"

	if _, err := discoverer.Discover(context.Background(), writeTempFile(t, "a.yml", excluded)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := discoverer.Discover(context.Background(), writeTempFile(t, "b.yml", kept)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := discoverer.Filtered(); len(got) != 0 {
		t.Errorf("expected no filters on the second run, got %+v", got)
	}
}

// fixedDiscoverer is a Discoverer that reports no filters of its own.
type fixedDiscoverer struct {
	updates []core.ImageUpdate
}

func (d fixedDiscoverer) Discover(context.Context, string) ([]core.ImageUpdate, error) {
	return d.updates, nil
}

// failingDiscoverer always errors.
type failingDiscoverer struct{}

func (failingDiscoverer) Discover(context.Context, string) ([]core.ImageUpdate, error) {
	return nil, errDiscovery
}

var errDiscovery = errors.New("cannot read file")
