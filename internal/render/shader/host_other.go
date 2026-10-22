//go:build !darwin && !windows

package shader

import (
	"fmt"
	"wpapp/internal/render"
)

func runHost(pipeIO) error {
	return fmt.Errorf("%w: native shader backend unavailable on this platform", render.ErrUnsupported)
}

func platformPreview(loadRequest, string) error { return render.ErrUnsupported }
func nativeTarget() Target                      { return HLSL }
