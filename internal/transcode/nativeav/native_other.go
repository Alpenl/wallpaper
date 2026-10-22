//go:build !darwin

package nativeav

import "context"

func Available() bool { return false }

func Encode(ctx context.Context, src, dst string, width, height int, cover ...bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return ErrUnavailable
}
