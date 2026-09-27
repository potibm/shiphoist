package core

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestReport_SerialisesWithFullDetail(t *testing.T) {
	report := Report{
		File:       "docker-compose.yml",
		Checked:    2,
		References: 3,
		ElapsedMS:  4210,
		Written:    true,
		Updates: []ImageUpdate{{
			FilePath:       "docker-compose.yml",
			LineNumber:     12,
			ServiceName:    "db",
			OriginalString: "postgres:16.2",
			ImageName:      "postgres",
			OldTag:         "16.2",
			OldDigest:      "sha256:old",
			NewTag:         "16.3",
			NewDigest:      "sha256:new",
			UpdateType:     UpdateTypeMinor,
			Selected:       true,
			MajorTag:       "17.0.0",
		}},
		Failures: []Failure{{Image: "[web] nginx", Message: "unauthorized"}},
		Filtered: []Filtered{{Image: "postgres", LineNumber: 4, Reason: "ignore-directive"}},
	}

	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}

	out := string(encoded)

	// The keys are a public contract for CI consumers, so they are asserted
	// rather than left to whatever the struct happens to be called.
	for _, want := range []string{
		`"file":"docker-compose.yml"`,
		`"checked":2`,
		`"references":3`,
		`"elapsed_ms":4210`,
		`"written":true`,
		`"dry_run":false`,
		`"line_number":12`,
		`"service_name":"db"`,
		`"original_string":"postgres:16.2"`,
		`"old_digest":"sha256:old"`,
		`"new_digest":"sha256:new"`,
		`"update_type":"minor"`,
		`"selected":true`,
		`"major_tag":"17.0.0"`,
		`"old_tag_missing":false`,
		`"no_compatible_tags":false`,
		`"image":"[web] nginx"`,
		`"message":"unauthorized"`,
		`"filtered":[{"image":"postgres","line_number":4,"reason":"ignore-directive"}]`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %s in:\n%s", want, out)
		}
	}
}

// A zero Report must still serialise to arrays rather than null, so a consumer
// never has to handle both shapes.
func TestReport_EmptySlicesSerialiseAsArrays(t *testing.T) {
	encoded, err := json.Marshal(Report{})
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}

	out := string(encoded)

	for _, want := range []string{`"updates":[]`, `"failures":[]`, `"filtered":[]`} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %s in:\n%s", want, out)
		}
	}
}
