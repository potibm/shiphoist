package core

import "testing"

func TestImageUpdate_Key(t *testing.T) {
	tests := []struct {
		name     string
		update   ImageUpdate
		expected string
	}{
		{
			name:     "file and line are the identity",
			update:   ImageUpdate{FilePath: "docker-compose.yml", LineNumber: 4},
			expected: "docker-compose.yml:4",
		},
		{
			name:     "falls back to the image name when unlocated",
			update:   ImageUpdate{ImageName: "nginx"},
			expected: "nginx",
		},
		{
			name:     "line without a file still keys on the line",
			update:   ImageUpdate{LineNumber: 7, ImageName: "nginx"},
			expected: ":7",
		},
		{
			name:     "fully empty",
			update:   ImageUpdate{},
			expected: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.update.Key(); got != tc.expected {
				t.Errorf("Key() = %q, want %q", got, tc.expected)
			}
		})
	}
}

// The whole point of Key is that two services on the same repository stay
// separate, so the keys must differ even though the image names match.
func TestImageUpdate_Key_SameRepoDifferentServices(t *testing.T) {
	db := ImageUpdate{FilePath: "docker-compose.yml", LineNumber: 4, ServiceName: "db", ImageName: "postgres"}
	cache := ImageUpdate{FilePath: "docker-compose.yml", LineNumber: 9, ServiceName: "cache", ImageName: "postgres"}

	if db.Key() == cache.Key() {
		t.Errorf("expected distinct keys for %q and %q, both were %q", db.ServiceName, cache.ServiceName, db.Key())
	}
}

func TestImageUpdate_Label(t *testing.T) {
	tests := []struct {
		name     string
		update   ImageUpdate
		expected string
	}{
		{
			name:     "with a service name",
			update:   ImageUpdate{ServiceName: "web", ImageName: "nginx"},
			expected: "[web] nginx",
		},
		{
			name:     "without a service name",
			update:   ImageUpdate{ImageName: "nginx"},
			expected: "nginx",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.update.Label(); got != tc.expected {
				t.Errorf("Label() = %q, want %q", got, tc.expected)
			}
		})
	}
}
