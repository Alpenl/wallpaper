package update

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func transactionFixture(t *testing.T) (*Client, *Manifest, map[string]string) {
	t.Helper()
	original := map[string]string{"a": "old a", "lib/b": "old b", "gone": "removed", "wallpaperd": "old executable", "unchanged": "same"}
	next := map[string]string{"a": "new a", "lib/b": "new b", "added": "new", "wallpaperd": "new executable", "unchanged": "same"}
	dir, release := t.TempDir(), t.TempDir()
	for p, b := range original {
		write(t, dir, p, b)
	}
	for p, b := range next {
		write(t, release, p, b)
	}
	m, err := BuildManifest(release, "0.2.0", "linux-amd64", "")
	if err != nil {
		t.Fatal(err)
	}
	m.Remove = []string{"gone"}
	c := &Client{Dir: dir, Current: "0.1.0", Feed: "https://update.invalid", Platform: "linux-amd64",
		HTTP: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
			b, ok := next[strings.TrimPrefix(r.URL.Path, "/linux-amd64/")]
			if !ok {
				return nil, fmt.Errorf("unexpected request %s", r.URL)
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(b)), Header: make(http.Header)}, nil
		})}}
	return c, m, original
}

func installSnapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p == filepath.Join(dir, stateDir) {
				return filepath.SkipDir
			}
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func assertRecovered(t *testing.T, c *Client, original map[string]string) {
	t.Helper()
	fresh := &Client{Dir: c.Dir}
	if err := fresh.Recover(); err != nil {
		t.Fatal(err)
	}
	if got := installSnapshot(t, c.Dir); !reflect.DeepEqual(got, original) {
		t.Fatalf("install = %v, want %v", got, original)
	}
	for _, name := range []string{pendingName, "rollback"} {
		if _, err := os.Stat(c.sd(name)); !os.IsNotExist(err) {
			t.Fatalf("%s remains: %v", name, err)
		}
	}
}

var errInjected = errors.New("injected interruption")

// Once interrupted, no subsequent seam operation may mutate the disk.
func injectTransactionFailure(c *Client, k int) *int {
	n := new(int)
	step := func() error {
		*n++
		if *n >= k {
			return errInjected
		}
		return nil
	}
	c.rename = func(a, b string) error {
		if err := step(); err != nil {
			return err
		}
		return os.Rename(a, b)
	}
	c.writeState = func(p *Pending) error {
		if err := step(); err != nil {
			return err
		}
		return c.persistPending(p)
	}
	return n
}

func TestTransactionApplyEveryStep(t *testing.T) {
	// Three replacements, one addition, one removal: eight renames + two writes.
	for k := 1; k <= 10; k++ {
		t.Run(fmt.Sprint(k), func(t *testing.T) {
			c, m, original := transactionFixture(t)
			n := injectTransactionFailure(c, k)
			if _, err := c.Apply(context.Background(), m, nil); !errors.Is(err, errInjected) {
				t.Fatalf("step %d: %v", k, err)
			}
			if *n != k {
				t.Fatalf("continued after failure: %d", *n)
			}
			assertRecovered(t, c, original)
		})
	}
}

func TestTransactionRecoverEveryStep(t *testing.T) {
	// Fully replaced but awaiting write interrupted; recover write + eight renames.
	for k := 1; k <= 9; k++ {
		t.Run(fmt.Sprint(k), func(t *testing.T) {
			c, m, original := transactionFixture(t)
			injectTransactionFailure(c, 10)
			if _, err := c.Apply(context.Background(), m, nil); !errors.Is(err, errInjected) {
				t.Fatal(err)
			}
			fresh := &Client{Dir: c.Dir}
			injectTransactionFailure(fresh, k)
			if err := fresh.Recover(); !errors.Is(err, errInjected) {
				t.Fatalf("step %d: %v", k, err)
			}
			before := installSnapshot(t, c.sd("rollback"))
			if _, err := c.Apply(context.Background(), m, nil); err == nil {
				t.Fatal("accepted incomplete recovery")
			}
			if !reflect.DeepEqual(before, installSnapshot(t, c.sd("rollback"))) {
				t.Fatal("rollback modified")
			}
			assertRecovered(t, c, original)
		})
	}
}

func TestTransactionRefusalAndCorruption(t *testing.T) {
	for _, state := range []string{"awaiting", "applying", "rolling_back", "corrupt", "unreadable"} {
		t.Run(state, func(t *testing.T) {
			c, m, _ := transactionFixture(t)
			write(t, c.Dir, ".update/rollback/sentinel", "preserve")
			switch state {
			case "corrupt":
				write(t, c.Dir, ".update/pending.json", "{bad")
			case "unreadable":
				if err := os.Mkdir(c.sd(pendingName), 0o755); err != nil {
					t.Fatal(err)
				}
			default:
				if err := c.persistPending(&Pending{From: "0.1.0", To: "0.2.0", State: state, Plan: []PlanEntry{}}); err != nil {
					t.Fatal(err)
				}
			}
			before := installSnapshot(t, c.sd())
			if _, err := c.Apply(context.Background(), m, nil); err == nil {
				t.Fatal("accepted pending")
			}
			if !reflect.DeepEqual(before, installSnapshot(t, c.sd())) {
				t.Fatal("state modified")
			}
			if state == "corrupt" || state == "unreadable" {
				if err := c.Recover(); err == nil {
					t.Fatal("recovery hid corruption")
				}
				if _, err := c.Boot(); err == nil {
					t.Fatal("boot hid corruption")
				}
				if err := c.MarkGood(); err == nil {
					t.Fatal("confirmation hid corruption")
				}
				if !reflect.DeepEqual(before, installSnapshot(t, c.sd())) {
					t.Fatal("corrupt state modified")
				}
			}
		})
	}
}

func TestTransactionLegacyPending(t *testing.T) {
	for _, good := range []bool{false, true} {
		t.Run(fmt.Sprint(good), func(t *testing.T) {
			c := &Client{Dir: t.TempDir()}
			if p, err := c.ReadPending(); p != nil || err != nil {
				t.Fatal(p, err)
			}
			extra := ""
			want := "awaiting"
			if good {
				extra = `,"good":true`
				want = "good"
			}
			write(t, c.Dir, ".update/pending.json", `{"from":"0.1.0","to":"0.2.0"`+extra+`}`)
			p, err := c.ReadPending()
			if err != nil || p.State != want {
				t.Fatal(p, err)
			}
			if _, err := c.Boot(); err != nil {
				t.Fatal(err)
			}
			if err := c.MarkGood(); err != nil {
				t.Fatal(err)
			}
			p, err = c.ReadPending()
			if err != nil || p.State != "good" {
				t.Fatal(p, err)
			}
		})
	}
}

func TestTransactionHappyPathAndExeLast(t *testing.T) {
	c, m, original := transactionFixture(t)
	var moves []string
	c.rename = func(a, b string) error {
		p, err := c.ReadPending()
		if err != nil || p == nil || p.State != "applying" || len(p.Plan) != 5 {
			t.Fatalf("missing complete intent: %+v %v", p, err)
		}
		moves = append(moves, a)
		return os.Rename(a, b)
	}
	if _, err := c.Apply(context.Background(), m, nil); err != nil {
		t.Fatal(err)
	}
	if len(moves) != 8 || filepath.Base(moves[6]) != "wallpaperd" || filepath.Base(moves[7]) != "wallpaperd" {
		t.Fatal(moves)
	}
	c.rename = nil
	if err := c.Recover(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < MaxBoots; i++ {
		if rb, err := c.Boot(); rb || err != nil {
			t.Fatal(rb, err)
		}
	}
	if err := c.MarkGood(); err != nil {
		t.Fatal(err)
	}
	if rb, err := c.Boot(); rb || err != nil {
		t.Fatal(rb, err)
	}
	if err := c.Rollback(); err != nil {
		t.Fatal(err)
	}
	assertRecovered(t, c, original)
}

func TestTransactionLegacyRollback(t *testing.T) {
	c := &Client{Dir: t.TempDir()}
	write(t, c.Dir, "a", "new")
	write(t, c.Dir, "added", "new")
	write(t, c.Dir, ".update/rollback/a", "old")
	write(t, c.Dir, ".update/rollback/gone", "removed")
	write(t, c.Dir, ".update/pending.json", `{"from":"0.1.0","to":"0.2.0","changed":["a","added"],"added":["added"],"removed":["gone"],"good":true}`)
	if err := c.Rollback(); err != nil {
		t.Fatal(err)
	}
	assertRecovered(t, c, map[string]string{"a": "old", "gone": "removed"})
}

func TestTransactionRestoreJoinsErrors(t *testing.T) {
	c, m, original := transactionFixture(t)
	if _, err := c.Apply(context.Background(), m, nil); err != nil {
		t.Fatal(err)
	}
	one, two := errors.New("first rename"), errors.New("second rename")
	n := 0
	c.rename = func(a, b string) error {
		n++
		if n == 1 {
			return one
		}
		if n == 2 {
			return two
		}
		return os.Rename(a, b)
	}
	err := c.Rollback()
	if !errors.Is(err, one) || !errors.Is(err, two) {
		t.Fatalf("lost restore errors: %v", err)
	}
	assertRecovered(t, c, original)
}

func TestTransactionMissingOriginalIsError(t *testing.T) {
	c := &Client{Dir: t.TempDir()}
	p := &Pending{From: "0.1.0", To: "0.2.0", State: "rolling_back", Plan: []PlanEntry{{Path: "missing", Existed: true}}}
	if err := c.persistPending(p); err != nil {
		t.Fatal(err)
	}
	if err := c.Recover(); err == nil {
		t.Fatal("missing original silently ignored")
	}
	if p, err := c.ReadPending(); err != nil || p == nil {
		t.Fatal("lost recovery record", err)
	}
}

func TestTransactionRenameNeverCopies(t *testing.T) {
	c := &Client{Dir: t.TempDir(), rename: func(string, string) error { return errInjected }}
	write(t, c.Dir, "src", "original")
	if err := c.moveFile(filepath.Join(c.Dir, "src"), filepath.Join(c.Dir, "dst")); !errors.Is(err, errInjected) {
		t.Fatal(err)
	}
	if read(t, c.Dir, "src") != "original" || read(t, c.Dir, "dst") != "<missing>" {
		t.Fatal("rename failure mutated files")
	}
}

func TestTransactionBootRollback(t *testing.T) {
	c, m, original := transactionFixture(t)
	if _, err := c.Apply(context.Background(), m, nil); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < MaxBoots; i++ {
		if rb, err := c.Boot(); rb || err != nil {
			t.Fatal(rb, err)
		}
	}
	if rb, err := c.Boot(); !rb || err != nil {
		t.Fatal(rb, err)
	}
	assertRecovered(t, c, original)
}
