package approve

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// TestReviewAppManifest checks that the review App asks for the approval
// permission and nothing it does not use.
func TestReviewAppManifest(t *testing.T) {
	data, err := os.ReadFile("../../deploy/review-app-manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Name               string            `json:"name"`
		URL                string            `json:"url"`
		Public             bool              `json:"public"`
		HookAttributes     any               `json:"hook_attributes"`
		DefaultPermissions map[string]string `json:"default_permissions"`
		DefaultEvents      []string          `json:"default_events"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"contents": "read", "metadata": "read", "pull_requests": "write"}
	if !reflect.DeepEqual(m.DefaultPermissions, want) {
		t.Errorf("default_permissions = %v, want %v", m.DefaultPermissions, want)
	}
	if m.DefaultEvents == nil || len(m.DefaultEvents) != 0 || m.HookAttributes != nil || m.Public || m.Name == "" || m.URL == "" {
		t.Errorf("manifest = %+v, want a private App with a name, a url, no webhook and no events", m)
	}
}
