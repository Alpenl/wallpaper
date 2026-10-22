package web

// Suspension separates native calls so their security-sensitive order can be
// checked without a Windows runtime. A failed hide must never attempt suspend.
type suspension interface {
	Capture() error
	Visible(bool) error
	Suspend() error
	Resume() error
}

func pauseNative(view suspension, paused bool) error {
	if !paused {
		if err := view.Resume(); err != nil {
			return err
		}
		return view.Visible(true)
	}
	if err := view.Capture(); err != nil {
		return err
	}
	if err := view.Visible(false); err != nil {
		return err
	}
	return view.Suspend()
}
