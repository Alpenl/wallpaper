package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"wpapp/internal/transcode/nativeav"
)

// Encoding and GIF parsing stay out of the controller/UI process. Cancellation
// of the caller's CommandContext terminates this helper; it has no UI or app
// initialization and cannot recursively select another encoder.
func runTranscodeHelper(f *flags) int {
	if f.in == "" || f.out == "" || f.width < 2 || f.height < 2 || f.width > 8192 || f.height > 8192 {
		fmt.Fprintln(os.Stderr, "transcode helper: input, output and target dimensions 2..8192 are required")
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	if err := nativeav.Encode(ctx, f.in, f.out, f.width, f.height, f.cover); err != nil {
		fmt.Fprintln(os.Stderr, "transcode helper:", err)
		return 3
	}
	return 0
}
