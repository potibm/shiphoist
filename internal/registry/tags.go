package registry

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Masterminds/semver/v3"
)

var (
	jep223Pattern  = regexp.MustCompile(`^v?(\d+)(?:\.(\d+)(?:\.(\d+))?)?_(\d+)(-.*)?$`)
	buildIDPattern = regexp.MustCompile(`-(?:ls|r)?(\d+)$`)
)

// Tag represents a single parsed Docker image tag.
type Tag struct {
	Raw        string          // The original string (e.g., "1.25.0-alpine")
	SemVer     *semver.Version // The parsed semantic version
	Suffix     string          // The flavor/variant suffix (e.g., "-alpine")
	BaseSuffix string          // Suffix with trailing build/revision IDs stripped (e.g., "-debian-11")
	BuildID    int             // Numeric build/revision ID extracted from suffix (0 if none)
	Precision  int             // Number of blocks of semver
	HasVPrefix bool            // Whether the tag starts with "v" (e.g., "v1.25.0")
}

// ParseTag takes a raw docker tag and splits it into its logical components.
// Returns an error if the tag cannot be parsed as valid SemVer.
// Supports JEP-223 style tags like "17.0.6_10-jre" by normalizing to "17.0.6-10+jre".
func ParseTag(raw string) (Tag, error) {
	normalized := raw
	isJEP223 := false

	if m := jep223Pattern.FindStringSubmatch(raw); m != nil {
		major := m[1]
		minor := m[2]
		patch := m[3]
		build := m[4]
		variant := m[5]

		if minor == "" {
			minor = "0"
		}
		if patch == "" {
			patch = "0"
		}

		normalized = major + "." + minor + "." + patch + "-" + build
		if variant != "" {
			normalized += "+" + strings.TrimPrefix(variant, "-")
		}
		isJEP223 = true
	}

	v, err := semver.NewVersion(normalized)
	if err != nil {
		return Tag{}, err
	}

	suffix := ""
	if !isJEP223 {
		dashIndex := strings.Index(raw, "-")
		if dashIndex != -1 {
			suffix = raw[dashIndex:]
		}
	} else {
		dashIndex := strings.Index(raw, "-")
		if dashIndex != -1 {
			suffix = raw[dashIndex:]
		}
	}

	baseSuffix := suffix
	buildID := 0

	if isJEP223 {
		if m := jep223Pattern.FindStringSubmatch(raw); m != nil {
			buildID, _ = strconv.Atoi(m[4])
		}
	} else {
		if m := buildIDPattern.FindStringSubmatch(suffix); m != nil {
			baseSuffix = suffix[:len(suffix)-len(m[0])]
			buildID, _ = strconv.Atoi(m[1])
		}
	}

	coreVersion := strings.TrimPrefix(strings.TrimSuffix(raw, suffix), "v")
	if isJEP223 {
		if m := jep223Pattern.FindStringSubmatch(raw); m != nil {
			coreVersion = m[1]
			if m[2] != "" {
				coreVersion += "." + m[2]
			}
			if m[3] != "" {
				coreVersion += "." + m[3]
			}
		}
	}
	precision := strings.Count(coreVersion, ".") + 1
	hasVPrefix := strings.HasPrefix(raw, "v")

	return Tag{
		Raw:        raw,
		SemVer:     v,
		Suffix:     suffix,
		BaseSuffix: baseSuffix,
		BuildID:    buildID,
		Precision:  precision,
		HasVPrefix: hasVPrefix,
	}, nil
}

// TagList is a slice of Tag structs, allowing custom methods.
type TagList []Tag

// NewTagListFromStrings takes a slice of raw tag strings, parses them,
// and returns a TagList containing only the valid semantic versions.
func NewTagListFromStrings(rawTags []string) TagList {
	var tags TagList
	for _, raw := range rawTags {
		parsed, err := ParseTag(raw)
		if err == nil {
			tags = append(tags, parsed)
		}
	}
	return tags
}

// FilterBySuffix returns a new TagList containing only tags with the exact suffix.
func (t TagList) FilterBySuffix(suffix string) TagList {
	var filtered TagList
	for _, tag := range t {
		if tag.Suffix == suffix {
			filtered = append(filtered, tag)
		}
	}
	return filtered
}

// FilterByBaseSuffix returns a new TagList containing only tags with matching base suffix
// (suffix with trailing build/revision IDs stripped).
func (t TagList) FilterByBaseSuffix(baseSuffix string) TagList {
	var filtered TagList
	for _, tag := range t {
		if tag.BaseSuffix == baseSuffix {
			filtered = append(filtered, tag)
		}
	}
	return filtered
}

func (t TagList) FilterByPrecision(precision int) TagList {
	var filtered TagList
	for _, tag := range t {
		if tag.Precision == precision {
			filtered = append(filtered, tag)
		}
	}
	return filtered
}

// FilterByVPrefix returns a new TagList containing only tags with matching v-prefix style.
func (t TagList) FilterByVPrefix(hasVPrefix bool) TagList {
	var filtered TagList
	for _, tag := range t {
		if tag.HasVPrefix == hasVPrefix {
			filtered = append(filtered, tag)
		}
	}
	return filtered
}

// SortBySemver sorts the tags in ascending order (oldest to newest) based on their SemVer.
// Ties are broken by BuildID (higher is newer).
// It returns a new sorted TagList to avoid mutating the original slice.
func (t TagList) SortBySemver() TagList {
	sorted := make(TagList, len(t))
	copy(sorted, t)

	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].SemVer.Equal(sorted[j].SemVer) {
			return sorted[i].BuildID < sorted[j].BuildID
		}
		return sorted[i].SemVer.LessThan(sorted[j].SemVer)
	})

	return sorted
}
