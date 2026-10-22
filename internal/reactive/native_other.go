//go:build !windows && !darwin

package reactive

import "fmt"

type unsupported struct{}

func (unsupported) Start() error                  { return fmt.Errorf("reactive: unsupported platform") }
func (unsupported) Stop()                         {}
func (unsupported) Read() ([]float32, int, error) { return nil, 0, nil }
func (unsupported) Sample() PointerSample         { return PointerSample{} }
func newAudio() Source                            { return unsupported{} }
func newPointer() Pointer                         { return unsupported{} }
