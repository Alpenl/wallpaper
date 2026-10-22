package web

import (
	"errors"
	"reflect"
	"testing"
)

type suspensionFake struct {
	steps []string
	fail  string
}

func (f *suspensionFake) step(s string) error {
	f.steps = append(f.steps, s)
	if f.fail == s {
		return errors.New(s)
	}
	return nil
}
func (f *suspensionFake) Capture() error { return f.step("capture") }
func (f *suspensionFake) Visible(on bool) error {
	if on {
		return f.step("show")
	}
	return f.step("hide")
}
func (f *suspensionFake) Suspend() error { return f.step("suspend") }
func (f *suspensionFake) Resume() error  { return f.step("resume") }
func TestSuspendOrderAndFailures(t *testing.T) {
	f := &suspensionFake{}
	if e := pauseNative(f, true); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(f.steps, []string{"capture", "hide", "suspend"}) {
		t.Fatal(f.steps)
	}
	f.steps = nil
	if e := pauseNative(f, false); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(f.steps, []string{"resume", "show"}) {
		t.Fatal(f.steps)
	}
	for _, stage := range []string{"capture", "hide"} {
		f := &suspensionFake{fail: stage}
		if pauseNative(f, true) == nil {
			t.Fatal("failure ignored")
		}
		for _, s := range f.steps {
			if s == "suspend" {
				t.Fatal("suspended after failure")
			}
		}
	}
}
