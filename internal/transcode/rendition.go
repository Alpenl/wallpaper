package transcode

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"wpapp/internal/media"
)

// Rendition describes a versioned center-cover transform in oriented display
// coordinates. Source includes the imported identity and its file revision.
// Equal transforms share a key regardless of monitor identity or DPI scale.
type Rendition struct {
	Source  string `json:"source"`
	Target  Target `json:"target"`
	Crop    string `json:"crop"`
	Version int    `json:"version"`
}

func CoverRendition(source string, width, height int) Rendition {
	return Rendition{source, (Target{width, height}).normalized(), "center-cover", 1}
}
func (r Rendition) Key() string {
	b, _ := json.Marshal(r)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (r Rendition) Dir(cache, item string) string {
	return filepath.Join(cache, item, "renditions", r.Key())
}
func (r Rendition) Path(cache, item string) string {
	return filepath.Join(r.Dir(cache, item), "video.mp4")
}

// Ready trusts only a matching manifest published after strict verification.
// Missing/truncated files are cache misses, never a reason to lose the source.
func (r Rendition) Ready(cache, item string) bool {
	b, err := os.ReadFile(filepath.Join(r.Dir(cache, item), "manifest.json"))
	if err != nil {
		return false
	}
	var m struct {
		Rendition Rendition
		Bytes     int64
	}
	if json.Unmarshal(b, &m) != nil || m.Rendition != r {
		return false
	}
	st, err := os.Lstat(r.Path(cache, item))
	return err == nil && st.Mode().IsRegular() && st.Size() == m.Bytes && m.Bytes > 0
}
func (r Rendition) Publish(cache, item, tmp string) error {
	dir := r.Dir(cache, item)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	st, err := os.Stat(tmp)
	if err != nil {
		return err
	}
	if err = os.Rename(tmp, r.Path(cache, item)); err != nil {
		return err
	}
	b, _ := json.Marshal(struct {
		Rendition Rendition
		Bytes     int64
	}{r, st.Size()})
	f, err := os.CreateTemp(dir, ".manifest-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(dir, "manifest.json"))
}
func VerifyCover(in *media.Info, target Target) error {
	if _, err := VerifyInfo(in, target); err != nil {
		return err
	}
	target = target.normalized()
	if in.W != target.Width || in.H != target.Height {
		return fmt.Errorf("cover output dimensions differ from target")
	}
	return nil
}

// CleanRenditions removes only keys outside the caller's pinned set.
func CleanRenditions(cache, item string, keep map[string]bool) error {
	dir := filepath.Join(cache, item, "renditions")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !keep[e.Name()] {
			if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}
