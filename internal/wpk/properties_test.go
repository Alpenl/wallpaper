package wpk

import (
	"encoding/json"
	"os"
	"testing"
)

func TestWebManifestProperties(t *testing.T) {
	b, e := os.ReadFile("../../examples/web-orbit/manifest.json")
	if e != nil {
		t.Fatal(e)
	}
	m, e := Parse(b)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = PropertyValues(m.Properties, `{"speed":1.5,"tint":"#123456","mode":"drift","showCaption":false,"caption":"Hello"}`); e != nil {
		t.Fatal(e)
	}
	for _, raw := range []string{`{"speed":3}`, `{"speed":"1"}`, `{"tint":"red"}`, `{"mode":"absent"}`, `{"showCaption":1}`, `{"caption":null}`, `{"bad":1}`, `null`, `[]`} {
		if _, e = PropertyValues(m.Properties, raw); e == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	for _, mutate := range []func(*Manifest){
		func(m *Manifest) { m.Properties = append(m.Properties, m.Properties[0]) },
		func(m *Manifest) { m.Properties[0].Default = json.RawMessage(`20`) },
		func(m *Manifest) { n := -1.; m.Properties[0].Step = &n },
		func(m *Manifest) { m.Web.Permissions = []string{"camera"} },
		func(m *Manifest) { m.Entry = "content/../index.html" },
		func(m *Manifest) { m.Web.FPSCap = 200 },
	} {
		x, _ := Parse(b)
		mutate(x)
		if x.Validate() == nil {
			t.Fatal("accepted invalid manifest")
		}
	}
}

func TestSignalPermissions(t *testing.T) {
	b, e := os.ReadFile("../../examples/web-orbit/manifest.json")
	if e != nil {
		t.Fatal(e)
	}
	for _, permissions := range [][]string{{"audio", "input"}, {"keyboard"}, {"audio", "audio"}} {
		m, _ := Parse(b)
		m.Permissions = permissions
		err := m.Validate()
		if permissions[0] == "audio" && len(permissions) == 2 && permissions[1] == "input" {
			if err != nil || !m.Declares("audio") || !m.Declares("input") {
				t.Fatal(m, err)
			}
		} else if err == nil {
			t.Fatal("accepted invalid permissions", permissions)
		}
	}
	m, _ := Parse(b)
	m.Web.Permissions = []string{"input"}
	if e := m.Validate(); e != nil || !m.Declares("input") {
		t.Fatal(e)
	}
	m.Type = "image"
	m.Permissions = []string{"audio"}
	if m.Validate() == nil {
		t.Fatal("image signal permission")
	}
}
