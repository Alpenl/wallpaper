package transcode

import "context"

// One encoder process per application, shared between required import conversion
// and background optimization. Callers may cancel while waiting for the slot.
var encoderSlot = make(chan struct{}, 1)

func serialEncoder(fn func(context.Context, string, string) error) func(context.Context, string, string) error {
	if fn == nil {
		return nil
	}
	return func(ctx context.Context, src, dst string) error {
		select {
		case encoderSlot <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
		defer func() { <-encoderSlot }()
		if err := ctx.Err(); err != nil {
			return err
		}
		return fn(ctx, src, dst)
	}
}
