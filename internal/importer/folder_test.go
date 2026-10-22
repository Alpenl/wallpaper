package importer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCollectSourcesPackagesRecursionAndLinks(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a.mp4", "nested/b.PNG", "nested/no.txt", "package/LivelyInfo.json", "package/video.mp4", ".hidden/c.mp4"} {
		p := filepath.Join(root, name)
		os.MkdirAll(filepath.Dir(p), 0755)
		os.WriteFile(p, []byte("{}"), 0600)
	}
	os.Symlink(filepath.Join(root, "a.mp4"), filepath.Join(root, "link.mp4"))
	got, err := CollectSources(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(root, "a.mp4"), filepath.Join(root, "nested/b.PNG"), filepath.Join(root, "package")}
	if len(got) != len(want) {
		t.Fatal(got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatal(got)
		}
	}
	got, err = CollectSources(filepath.Join(root, "package"))
	if err != nil || len(got) != 1 || got[0] != want[2] {
		t.Fatal(got, err)
	}
	if _, err := CollectSources(t.TempDir()); err == nil {
		t.Fatal("empty folder accepted")
	}
}

func TestCollectSourcesBoundsPackagesAndMedia(t *testing.T) {
	for _, names := range [][]string{
		{"a/manifest.json", "b/manifest.json"},
		{"a.mp4", "b.png"},
		{"a/manifest.json", "b.mp4"},
	} {
		t.Run(strings.Join(names, "+"), func(t *testing.T) {
			root := t.TempDir()
			for _, name := range names {
				p := filepath.Join(root, name)
				if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if got, err := collectSources(root, 1); err == nil || got != nil {
				t.Fatalf("expected bounded rejection: %v %v", got, err)
			}
			if got, err := collectSources(root, 2); err != nil || len(got) != 2 {
				t.Fatalf("exact limit rejected: %v %v", got, err)
			}
		})
	}
}
