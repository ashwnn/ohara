package project

import "testing"

func TestNormalizeRemote(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"git@github.com:ashwnn/ohara.git", "git@github.com:ashwnn/ohara"},
		{"https://github.com/ashwnn/ohara.git", "https://github.com/ashwnn/ohara"},
		{"https://github.com/ashwnn/ohara/", "https://github.com/ashwnn/ohara"},
	}
	for _, tt := range tests {
		if got := normalizeRemote(tt.in); got != tt.want {
			t.Fatalf("normalizeRemote(%q)=%q want %q", tt.in, got, tt.want)
		}
	}
}

func TestProjectIDDeterministicFromRemote(t *testing.T) {
	remoteA := "git@github.com:ashwnn/ohara.git"
	remoteB := "git@github.com:ashwnn/ohara"
	if shortHash(normalizeRemote(remoteA)) != shortHash(normalizeRemote(remoteB)) {
		t.Fatal("expected normalized remote hash to be deterministic")
	}
}

func TestSlug(t *testing.T) {
	if got := slug("My Repo_Name"); got != "my-repo-name" {
		t.Fatalf("slug mismatch: %q", got)
	}
}
