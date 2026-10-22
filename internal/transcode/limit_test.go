package transcode

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestEncoderSlotSerializesDifferentCallersAndCancelsWait(t *testing.T) {
	var active, max atomic.Int32
	fn := func(context.Context, string, string) error {
		n := active.Add(1)
		defer active.Add(-1)
		if n > max.Load() {
			max.Store(n)
		}
		time.Sleep(5 * time.Millisecond)
		return nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := serialEncoder(fn)(context.Background(), "", ""); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if max.Load() != 1 {
		t.Fatal(max.Load())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := serialEncoder(fn)(ctx, "", ""); err != context.Canceled {
		t.Fatal(err)
	}
}
