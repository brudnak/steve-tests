package main

import (
	"strings"
	"testing"
)

const lsRemote = `aaa	refs/tags/v0.9.9
bbb	refs/tags/v0.9.10
ccc	refs/tags/v0.9.11-rc.2
ddd	refs/tags/v0.9.11-rc.10
eee	refs/tags/v0.10.0-rc.1
fff	refs/tags/v0.10.0
ggg	refs/tags/v0.10.1-rc.1
hhh	refs/tags/v2.7.6-debug-41809-1
iii	refs/tags/v0.8.28
jjj	refs/tags/not-a-version
`

func TestLatestTags(t *testing.T) {
	got, err := LatestTags(strings.NewReader(lsRemote), []string{"0.9", "0.10"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		// rc.10 sorts after rc.2, and 0.9.11 prereleases beat 0.9.10.
		"0.9": "v0.9.11-rc.10",
		// A prerelease of the next patch is newer than the last release.
		"0.10": "v0.10.1-rc.1",
	}
	for minor, tag := range want {
		if got[minor] != tag {
			t.Errorf("minor %s: got %q, want %q", minor, got[minor], tag)
		}
	}
	if len(got) != len(want) {
		t.Errorf("got extra minors: %v", got)
	}
}

func TestReleaseBeatsItsPrerelease(t *testing.T) {
	got, err := LatestTags(strings.NewReader("v0.10.0-rc.1\nv0.10.0\n"), []string{"0.10"})
	if err != nil {
		t.Fatal(err)
	}
	if got["0.10"] != "v0.10.0" {
		t.Errorf("got %q, want v0.10.0", got["0.10"])
	}
}

func TestLatestTagsMissingMinor(t *testing.T) {
	_, err := LatestTags(strings.NewReader(lsRemote), []string{"0.10", "0.11"})
	if err == nil || !strings.Contains(err.Error(), "0.11") {
		t.Fatalf("expected an error naming 0.11, got %v", err)
	}
}

func TestSortedMinorsIsNumeric(t *testing.T) {
	got := strings.Join(sortedMinors(map[string]string{"0.10": "", "0.9": "", "0.8": ""}), " ")
	if got != "0.8 0.9 0.10" {
		t.Errorf("got %q", got)
	}
}
