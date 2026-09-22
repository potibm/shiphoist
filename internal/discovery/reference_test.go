package discovery

import "testing"

func TestParseImageReference(t *testing.T) {
	tests := []struct {
		name     string
		ref      string
		wantName string
		wantTag  string
		wantDgst string
	}{
		{"bare image", "nginx", "nginx", "latest", ""},
		{"image with tag", "nginx:1.25", "nginx", "1.25", ""},
		{"image with digest", "nginx@sha256:abc123", "nginx", "latest", "sha256:abc123"},
		{"image with tag and digest", "nginx:1.25@sha256:abc123", "nginx", "1.25", "sha256:abc123"},
		{"registry with port and tag", "localhost:5000/foo:1.0", "localhost:5000/foo", "1.0", ""},
		{"registry with port no tag", "localhost:5000/foo", "localhost:5000/foo", "latest", ""},
		{
			"registry with port and digest",
			"localhost:5000/foo@sha256:def456",
			"localhost:5000/foo",
			"latest",
			"sha256:def456",
		},
		{"ghcr.io with tag", "ghcr.io/potibm/kasseapparat:2.18.0", "ghcr.io/potibm/kasseapparat", "2.18.0", ""},
		{"docker hub library", "docker.io/library/nginx:alpine", "docker.io/library/nginx", "alpine", ""},
		{"image with latest tag", "redis:latest", "redis", "latest", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotName, gotTag, gotDgst := ParseImageReference(tt.ref)
			if gotName != tt.wantName {
				t.Errorf("ParseImageReference(%q) name = %q, want %q", tt.ref, gotName, tt.wantName)
			}

			if gotTag != tt.wantTag {
				t.Errorf("ParseImageReference(%q) tag = %q, want %q", tt.ref, gotTag, tt.wantTag)
			}

			if gotDgst != tt.wantDgst {
				t.Errorf("ParseImageReference(%q) digest = %q, want %q", tt.ref, gotDgst, tt.wantDgst)
			}
		})
	}
}
