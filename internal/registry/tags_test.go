package registry

import (
	"testing"
)

func TestParseTag(t *testing.T) {
	tag, err := ParseTag("1.25.0-alpine")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tag.Raw != "1.25.0-alpine" {
		t.Errorf("expected Raw '1.25.0-alpine', got '%s'", tag.Raw)
	}
	if tag.Suffix != "-alpine" {
		t.Errorf("expected Suffix '-alpine', got '%s'", tag.Suffix)
	}
}

func TestParseTag_NoSuffix(t *testing.T) {
	tag, err := ParseTag("1.25.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tag.Suffix != "" {
		t.Errorf("expected empty Suffix, got '%s'", tag.Suffix)
	}
}

func TestTagList_FilterBySuffix(t *testing.T) {
	rawTags := []string{"1.25.0", "1.25.0-alpine", "1.26.0-alpine", "1.25.0-bullseye"}
	tags := NewTagListFromStrings(rawTags)

	filtered := tags.FilterBySuffix("-alpine")

	if len(filtered) != 2 {
		t.Fatalf("expected 2 filtered tags, got %d", len(filtered))
	}
	if filtered[0].Raw != "1.25.0-alpine" || filtered[1].Raw != "1.26.0-alpine" {
		t.Errorf("filtered list did not match expectations")
	}
}

func TestTagList_SortBySemver(t *testing.T) {
	// A messy list of tags in random order
	rawTags := []string{"1.26.0-alpine", "1.24.0-alpine", "1.25.2-alpine", "1.25.0-alpine"}
	tags := NewTagListFromStrings(rawTags)

	sorted := tags.SortBySemver()

	expectedRaw := []string{"1.24.0-alpine", "1.25.0-alpine", "1.25.2-alpine", "1.26.0-alpine"}

	if len(sorted) != len(expectedRaw) {
		t.Fatalf("expected length %d, got %d", len(expectedRaw), len(sorted))
	}

	for i, expected := range expectedRaw {
		if sorted[i].Raw != expected {
			t.Errorf("expected index %d to be %s, got %s", i, expected, sorted[i].Raw)
		}
	}
}

func TestParseTag_HasVPrefix(t *testing.T) {
	tag, err := ParseTag("v1.25.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !tag.HasVPrefix {
		t.Error("expected HasVPrefix to be true for v1.25.0")
	}

	tag2, err := ParseTag("1.25.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tag2.HasVPrefix {
		t.Error("expected HasVPrefix to be false for 1.25.0")
	}
}

func TestTagList_FilterByVPrefix(t *testing.T) {
	rawTags := []string{"v1.0.0", "v1.1.0", "1.2.0", "1.3.0"}
	tags := NewTagListFromStrings(rawTags)

	vPrefixed := tags.FilterByVPrefix(true)
	if len(vPrefixed) != 2 {
		t.Fatalf("expected 2 v-prefixed tags, got %d", len(vPrefixed))
	}
	if vPrefixed[0].Raw != "v1.0.0" || vPrefixed[1].Raw != "v1.1.0" {
		t.Errorf("v-prefixed filter did not match expectations")
	}

	noVPrefix := tags.FilterByVPrefix(false)
	if len(noVPrefix) != 2 {
		t.Fatalf("expected 2 non-v-prefixed tags, got %d", len(noVPrefix))
	}
	if noVPrefix[0].Raw != "1.2.0" || noVPrefix[1].Raw != "1.3.0" {
		t.Errorf("non-v-prefixed filter did not match expectations")
	}
}

func TestParseTag_JEP223(t *testing.T) {
	tag, err := ParseTag("17.0.6_10-jre")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tag.BuildID != 10 {
		t.Errorf("expected BuildID 10, got %d", tag.BuildID)
	}
	if tag.BaseSuffix != "-jre" {
		t.Errorf("expected BaseSuffix '-jre', got '%s'", tag.BaseSuffix)
	}
	if tag.Precision != 3 {
		t.Errorf("expected Precision 3, got %d", tag.Precision)
	}
}

func TestParseTag_JEP223_FeatureOnly(t *testing.T) {
	tag, err := ParseTag("17_35-jdk")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tag.BuildID != 35 {
		t.Errorf("expected BuildID 35, got %d", tag.BuildID)
	}
	if tag.Precision != 1 {
		t.Errorf("expected Precision 1, got %d", tag.Precision)
	}
}

func TestParseTag_LinuxServer(t *testing.T) {
	tag, err := ParseTag("25.0.4-ls212")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tag.BuildID != 212 {
		t.Errorf("expected BuildID 212, got %d", tag.BuildID)
	}
	if tag.BaseSuffix != "" {
		t.Errorf("expected empty BaseSuffix, got '%s'", tag.BaseSuffix)
	}
}

func TestParseTag_Bitnami(t *testing.T) {
	tag, err := ParseTag("15.2.0-debian-11-r10")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tag.BuildID != 10 {
		t.Errorf("expected BuildID 10, got %d", tag.BuildID)
	}
	if tag.BaseSuffix != "-debian-11" {
		t.Errorf("expected BaseSuffix '-debian-11', got '%s'", tag.BaseSuffix)
	}
}

func TestTagList_FilterByBaseSuffix(t *testing.T) {
	rawTags := []string{"25.0.4-ls212", "25.0.5-ls215", "25.0.6-ls220", "26.0.0-ls100"}
	tags := NewTagListFromStrings(rawTags)

	filtered := tags.FilterByBaseSuffix("")
	if len(filtered) != 4 {
		t.Fatalf("expected 4 tags with empty BaseSuffix, got %d", len(filtered))
	}
}

func TestTagList_SortBySemver_BuildID(t *testing.T) {
	rawTags := []string{"25.0.4-ls220", "25.0.4-ls212", "25.0.4-ls215"}
	tags := NewTagListFromStrings(rawTags)

	sorted := tags.SortBySemver()

	expectedBuildIDs := []int{212, 215, 220}
	for i, expected := range expectedBuildIDs {
		if sorted[i].BuildID != expected {
			t.Errorf("expected index %d to have BuildID %d, got %d", i, expected, sorted[i].BuildID)
		}
	}
}
