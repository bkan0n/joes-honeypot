package bot

import "testing"

func TestParseUnbanID(t *testing.T) {
	if id, ok := parseUnbanID("unban:42"); !ok || id != 42 {
		t.Fatalf("got %v %v", id, ok)
	}
	for _, bad := range []string{"unban:", "unban:abc", "moderated_count", ""} {
		if _, ok := parseUnbanID(bad); ok {
			t.Errorf("parseUnbanID(%q) should fail", bad)
		}
	}
}
