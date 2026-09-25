package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// filterMentions must stay fast and bounded on a large repo - a linear scan
// with no ranking was fine at a few hundred files, this proves it also holds
// at the scale pie's Android monorepo targets actually reach.
func TestFilterMentionsScalesToALargeRepo(t *testing.T) {
	files := make([]string, 50000)
	for i := range files {
		files[i] = fmt.Sprintf("app/src/main/java/com/example/pkg%d/File%d.kt", i%500, i)
	}
	lower := make([]string, len(files))
	for i, f := range files {
		lower[i] = strings.ToLower(f)
	}

	start := time.Now()
	items := filterMentions("zzz-does-not-exist", nil, files, lower, mentionPickerCap)
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("a no-match query over %d files took %s, want it bounded by mentionScanCap", len(files), elapsed)
	}
	if len(items) != 0 {
		t.Fatalf("no-match query returned %v, want none", items)
	}

	items = filterMentions("file1", nil, files, lower, mentionPickerCap)
	if len(items) == 0 || len(items) > mentionPickerCap {
		t.Fatalf("filterMentions returned %d items, want 1..%d", len(items), mentionPickerCap)
	}
}

// A basename match ("File42.kt") ranks ahead of a match that only exists in
// a directory segment ("filedir/Other.kt"), for the same query.
func TestFilterMentionsPrefersBasenameMatches(t *testing.T) {
	files := []string{"filedir/deep/Other.kt", "app/File42.kt"}
	lower := []string{strings.ToLower(files[0]), strings.ToLower(files[1])}

	items := filterMentions("file", nil, files, lower, mentionPickerCap)
	if len(items) != 2 || items[0] != "app/File42.kt" {
		t.Fatalf("filterMentions = %v, want the basename match first", items)
	}
}

// Priority entries (a change screen's already-reviewed files) always lead,
// regardless of how the repo matches would otherwise rank.
func TestFilterMentionsPriorityLeadsRegardlessOfRanking(t *testing.T) {
	files := []string{"app/File.kt"}
	lower := []string{strings.ToLower(files[0])}

	items := filterMentions("file", []string{"z/NotABasenameMatchFile.kt"}, files, lower, mentionPickerCap)
	if len(items) != 2 || items[0] != "z/NotABasenameMatchFile.kt" {
		t.Fatalf("filterMentions = %v, want the priority entry first", items)
	}
}

// repoFilesLower shorter than repoFiles (a caller that hasn't precomputed it
// yet) must still work, falling back to lowercasing that one path on the spot.
func TestFilterMentionsToleratesMissingLowercaseCache(t *testing.T) {
	items := filterMentions("one", nil, []string{"a/One.kt"}, nil, mentionPickerCap)
	if len(items) != 1 || items[0] != "a/One.kt" {
		t.Fatalf("filterMentions without a lowercase cache = %v, want the match", items)
	}
}
