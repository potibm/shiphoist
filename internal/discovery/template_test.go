package discovery

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/potibm/shiphoist/internal/core"
)

// A Compose file rendered by Ansible carries template syntax on most of its
// values. None of it is YAML, so before masking these files could not be read
// at all, even though every image reference in them is plain.
func TestComposeDiscoverer_MasksTemplateExpressions(t *testing.T) {
	content := `services:
  app:
    image: nginx:1.25.0
    environment:
      PASSWORD: "{{ vault.password }}"
      DOMAIN: {{ app_domain }}
      MIXED: https://{{ app_domain }}/path
    deploy:
      replicas: {{ item.value.worker_count }}
  db:
    image: postgres:16
`

	got := discover(t, content)

	expected := []core.ImageUpdate{
		{LineNumber: 3, ServiceName: "app", ImageName: "nginx", OldTag: "1.25.0", OriginalString: "nginx:1.25.0"},
		{LineNumber: 11, ServiceName: "db", ImageName: "postgres", OldTag: "16", OriginalString: "postgres:16"},
	}

	if len(got) != len(expected) {
		t.Fatalf("expected %d updates, got %d: %+v", len(expected), len(got), got)
	}

	for i, exp := range expected {
		assertUpdate(t, i, got[i], exp)
	}
}

// A `{% if %}` around part of the file is a line of its own. Dropping it has to
// leave valid YAML behind, whichever branch the renderer would take.
func TestComposeDiscoverer_DropsJinjaStatements(t *testing.T) {
	content := `services:
  app:
    image: nginx:1.25.0
    labels:
{% if item.value.auth | default(false) %}
      - "traefik.middlewares=auth"
{% else %}
      - "traefik.middlewares=none"
{% endif %}
{# a comment that is not YAML either #}
{{/* nor is a Go template comment */}}
  worker:
    image: redis:7.2
`

	got := discover(t, content)

	expected := []core.ImageUpdate{
		{LineNumber: 3, ServiceName: "app", ImageName: "nginx", OldTag: "1.25.0", OriginalString: "nginx:1.25.0"},
		{LineNumber: 13, ServiceName: "worker", ImageName: "redis", OldTag: "7.2", OriginalString: "redis:7.2"},
	}

	if len(got) != len(expected) {
		t.Fatalf("expected %d updates, got %d: %+v", len(expected), len(got), got)
	}

	for i, exp := range expected {
		assertUpdate(t, i, got[i], exp)
	}
}

// The other engines around Compose files have their own delimiters. All of them
// produce a value, so all of them have to survive as one.
func TestComposeDiscoverer_MasksOtherTemplateSyntaxes(t *testing.T) {
	content := `services:
  app:
    image: nginx:1.25.0
    environment:
      A: ${VAR}
      B: $VAR
      C: "prefix-${VAR}-suffix"
      D: <%= ruby_var %>
    ports:
      - "${PORT}:80"
    command: echo $HOME
`

	got := discover(t, content)

	if len(got) != 1 {
		t.Fatalf("expected 1 update, got %d: %+v", len(got), got)
	}

	assertUpdate(t, 0, got[0], core.ImageUpdate{
		LineNumber:     3,
		ServiceName:    "app",
		ImageName:      "nginx",
		OldTag:         "1.25.0",
		OriginalString: "nginx:1.25.0",
	})
}

// A reference that only exists after rendering cannot be resolved, and writing
// a digest into the expression would corrupt the template. It is reported as
// not checked, with the expression as it appears in the file.
func TestComposeDiscoverer_SkipsTemplatedReferences(t *testing.T) {
	content := `services:
  app:
    image: ${IMAGE:-ghcr.io/acme/app:latest}
  other:
    image: "{{ app_image }}"
  plain:
    image: nginx:1.25.0
`

	discoverer := &ComposeDiscoverer{}

	updates, err := discoverer.Discover(context.Background(), writeTempFile(t, "docker-compose.yml", content))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := services(t, updates); !reflect.DeepEqual(got, []string{"plain"}) {
		t.Errorf("expected only plain, got %v", got)
	}

	if updates[0].LineNumber != 7 {
		t.Errorf("expected line 7, got %d", updates[0].LineNumber)
	}

	filtered := discoverer.Filtered()
	if len(filtered) != 2 {
		t.Fatalf("expected 2 filtered references, got %+v", filtered)
	}

	if filtered[0].LineNumber != 3 || !strings.Contains(filtered[0].Image, "IMAGE:-ghcr.io/acme/app:latest") {
		t.Errorf("unexpected first filter: %+v", filtered[0])
	}

	if filtered[1].LineNumber != 5 || !strings.Contains(filtered[1].Image, "{{ app_image }}") {
		t.Errorf("unexpected second filter: %+v", filtered[1])
	}

	for _, f := range filtered {
		if f.Reason != ReasonTemplated {
			t.Errorf("expected reason %q, got %q", ReasonTemplated, f.Reason)
		}
	}
}

// A service key can be a template expression too. The update is still found,
// and the label names the expression rather than the placeholder.
func TestComposeDiscoverer_RestoresTemplatedServiceName(t *testing.T) {
	content := `services:
  "{{ item.key }}":
    image: nginx:1.25.0
`

	got := discover(t, content)

	if len(got) != 1 {
		t.Fatalf("expected 1 update, got %d: %+v", len(got), got)
	}

	if !strings.Contains(got[0].ServiceName, "{{ item.key }}") {
		t.Errorf("expected the expression as the service name, got %q", got[0].ServiceName)
	}

	if got[0].LineNumber != 3 {
		t.Errorf("expected line 3, got %d", got[0].LineNumber)
	}
}

// Masking must never add or remove a line, or every line number the patcher
// relies on would be off by the number of templates above it.
func TestTemplateMask_PreservesLineNumbers(t *testing.T) {
	content := `services:
  app:
    image: nginx:1.25.0
    environment:
      A: {{ a }}
      B: ${B}
      C: $C
{% if x %}
    labels:
      - "a={{ a }}"
{% endif %}
`

	masked := newTemplateMask().mask([]byte(content))

	got := strings.Count(string(masked), "\n")
	want := strings.Count(content, "\n")

	if got != want {
		t.Errorf("expected %d newlines, got %d:\n%s", want, got, masked)
	}

	if !hasTemplate(string(masked)) {
		t.Errorf("expected the expressions to be masked, got:\n%s", masked)
	}
}

// restore has to reproduce the expression exactly, including its delimiters, so
// a report names the same text as the file.
func TestTemplateMask_Restore(t *testing.T) {
	mask := newTemplateMask()

	masked := string(mask.mask([]byte(`image: ${IMAGE:-ghcr.io/acme/app:latest}`)))

	if !hasTemplate(masked) {
		t.Fatalf("expected the expression to be masked, got %q", masked)
	}

	restored := mask.restore(masked)

	if restored != `image: ${IMAGE:-ghcr.io/acme/app:latest}` {
		t.Errorf("unexpected restored text %q", restored)
	}
}
