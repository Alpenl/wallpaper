package transcode

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRenditionKeysAndManifest(t *testing.T) {
	r := CoverRendition("item:revision", 1920, 1080)
	if r.Key() != CoverRendition("item:revision", 1921, 1081).Key() {
		t.Fatal("even normalized targets should share")
	}
	for _, other := range []Rendition{CoverRendition("other:revision", 1920, 1080), CoverRendition("item:new", 1920, 1080), CoverRendition("item:revision", 1080, 1920)} {
		if r.Key() == other.Key() {
			t.Fatal("distinct identity/target collided")
		}
	}
	other := r
	other.Crop = "top-cover"
	if other.Key() == r.Key() {
		t.Fatal("crop omitted from key")
	}
	root := t.TempDir()
	tmp := filepath.Join(root, "tmp.mp4")
	if r.Ready(root, "x") {
		t.Fatal("missing cache ready")
	}
	os.WriteFile(tmp, []byte("verified output"), 0600)
	if err := r.Publish(root, "x", tmp); err != nil {
		t.Fatal(err)
	}
	if !r.Ready(root, "x") {
		t.Fatal("published cache not ready")
	}
	if err := CleanRenditions(root, "x", map[string]bool{r.Key(): true}); err != nil {
		t.Fatal(err)
	}
	if !r.Ready(root, "x") {
		t.Fatal("removed pinned rendition")
	}
	os.WriteFile(r.Path(root, "x"), []byte("truncated"), 0600)
	if r.Ready(root, "x") {
		t.Fatal("accepted truncated cache")
	}
	if err := CleanRenditions(root, "x", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(r.Dir(root, "x")); !os.IsNotExist(err) {
		t.Fatal("orphan remains", err)
	}
}

func TestCoverEncodesExactTargetReal(t *testing.T) {
	ff := realTools(t)
	for _, size := range []string{"180x640", "640x180", "64x36"} {
		t.Run(size, func(t *testing.T) {
			dir := t.TempDir()
			src, dst := filepath.Join(dir, "source.mkv"), filepath.Join(dir, "cover.mp4")
			out, err := exec.Command(ff, "-v", "error", "-f", "lavfi", "-i", "testsrc=size="+size+":rate=60:duration=1", "-c:v", "ffv1", src).CombinedOutput()
			if err != nil {
				t.Fatal(err, string(out))
			}
			target := Target{320, 180}
			fn, _, _ := CoverEncoder("linux", ff, "", target)
			if err := fn(context.Background(), src, dst); err != nil {
				t.Fatal(err)
			}
			info, err := VerifyOutput(dst, target)
			if err != nil {
				t.Fatal(err)
			}
			if err := VerifyCover(info, target); err != nil {
				t.Fatal(err)
			}
		})
	}
}
