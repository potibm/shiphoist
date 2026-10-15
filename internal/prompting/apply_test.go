package prompting

import (
	"slices"
	"testing"

	"github.com/potibm/shiphoist/internal/core"
)

// update builds a reference the way the fetcher hands it over: a major is never
// pre-selected, because a major has to be opted into.
func update(service string, updateType core.UpdateType) core.ImageUpdate {
	return core.ImageUpdate{
		FilePath:    "docker-compose.yml",
		LineNumber:  len(service) + 4,
		ServiceName: service,
		ImageName:   service,
		UpdateType:  updateType,
		Selected:    core.Severity(updateType) < core.SeverityMajor,
	}
}

func TestApply_CapsBySeverity(t *testing.T) {
	updates := []core.ImageUpdate{
		update("pin", core.UpdateTypeNone),
		update("patched", core.UpdateTypePatch),
		update("minored", core.UpdateTypeMinor),
		update("majored", core.UpdateTypeMajor),
	}

	tests := []struct {
		name string
		cap  core.UpdateType
		want []string
	}{
		// A major is not pre-selected, so it stays out even with no cap.
		{name: "no cap", cap: "", want: []string{"pin", "patched", "minored"}},
		{name: "patch cap", cap: core.UpdateTypePatch, want: []string{"pin", "patched"}},
		{name: "minor cap", cap: core.UpdateTypeMinor, want: []string{"pin", "patched", "minored"}},
		// --mode major is not an opt-in to apply majors automatically.
		{name: "major cap", cap: core.UpdateTypeMajor, want: []string{"pin", "patched", "minored"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Apply{MaxUpdate: tt.cap}.SelectUpdates(updates)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if services := names(got); !slices.Equal(services, tt.want) {
				t.Errorf("expected %v, got %v", tt.want, services)
			}
		})
	}
}

// The ceiling below a major is what makes --yes safe, so it has to hold even
// for an update the fetcher marks selectable by mistake. Depending on the
// fetcher alone would make the guarantee break silently the day that changes.
func TestApply_NeverAppliesAMajor(t *testing.T) {
	major := update("majored", core.UpdateTypeMajor)
	major.Selected = true

	for _, cap := range []core.UpdateType{"", core.UpdateTypeMinor, core.UpdateTypeMajor} {
		got, err := Apply{MaxUpdate: cap}.SelectUpdates([]core.ImageUpdate{major})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(got) != 0 {
			t.Errorf("cap %q: a major was applied: %v", cap, names(got))
		}
	}
}

func TestApply_SkipsUnselected(t *testing.T) {
	patch := update("patched", core.UpdateTypePatch)
	patch.Selected = false

	got, err := Apply{}.SelectUpdates([]core.ImageUpdate{patch})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(got) != 0 {
		t.Errorf("expected nothing applied, got %v", names(got))
	}
}

// A cap that excludes everything must yield an empty, non-nil slice, so the
// engine can tell "nothing to apply" from "nothing was resolved".
func TestApply_CapExcludesEverything(t *testing.T) {
	updates := []core.ImageUpdate{update("minored", core.UpdateTypeMinor)}

	got, err := Apply{MaxUpdate: core.UpdateTypePatch}.SelectUpdates(updates)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(got) != 0 {
		t.Errorf("expected nothing applied, got %v", names(got))
	}

	if got == nil {
		t.Error("expected an empty slice, not nil")
	}
}

func TestApply_EmptyInput(t *testing.T) {
	got, err := Apply{}.SelectUpdates(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(got) != 0 {
		t.Errorf("expected nothing, got %v", names(got))
	}
}

func names(updates []core.ImageUpdate) []string {
	out := make([]string, 0, len(updates))
	for _, u := range updates {
		out = append(out, u.ServiceName)
	}

	return out
}
