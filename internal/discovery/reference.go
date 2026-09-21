package discovery

import "strings"

// ParseImageReference splits a Docker image reference into its components.
// Supports formats: "image", "image:tag", "image@sha256:...", "image:tag@sha256:...",
// and registry-with-port like "localhost:5000/foo:1.0".
func ParseImageReference(ref string) (name, tag, digest string) {
	remaining := ref

	if idx := strings.Index(remaining, "@"); idx != -1 {
		digest = remaining[idx+1:]
		remaining = remaining[:idx]
	}

	if idx := strings.LastIndex(remaining, "/"); idx != -1 {
		if tagIdx := strings.Index(remaining[idx:], ":"); tagIdx != -1 {
			absTagIdx := idx + tagIdx
			name = remaining[:absTagIdx]
			tag = remaining[absTagIdx+1:]

			return name, tag, digest
		}

		name = remaining
		tag = "latest"

		return name, tag, digest
	}

	if name, tag, ok = strings.Cut(remaining, ":"); ok { ... }

	name = remaining
	tag = "latest"

	return name, tag, digest
}
