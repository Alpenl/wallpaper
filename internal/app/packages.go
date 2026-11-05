package app

import "context"

// PreparePackageReplacement drains active renderers before a package directory
// is replaced or removed, so no player keeps reading files that are about to change.
func (a *App) PreparePackageReplacement(ctx context.Context, id string) error {
	queued := make(chan []<-chan struct{}, 1)
	a.Do(func() {
		var waits []<-chan struct{}
		for _, s := range a.sortedScreens() {
			if s.item != nil && s.item.ID == id {
				a.closeScreen(s, false)
				waits = append(waits, s.ops.barrier())
			}
		}
		queued <- waits
	})
	select {
	case waits := <-queued:
		for _, done := range waits {
			select {
			case <-done:
			case <-ctx.Done():
				return ctx.Err()
			case <-a.closing:
				return context.Canceled
			}
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-a.closing:
		return context.Canceled
	}
}
