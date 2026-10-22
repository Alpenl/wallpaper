package app

// ReportPersistenceError uses the existing LastError snapshot and event loop.
// A coalesced wake-up cannot block a saver callback during shutdown.
func (a *App) ReportPersistenceError(err error) {
	if err == nil {
		return
	}
	a.log.Error("config persistence failed", "err", err)
	message := err.Error()
	a.persistenceErr.Store(&message)
	select {
	case a.post <- func() {}:
	default:
	}
}

func (a *App) consumePersistenceError() {
	if message := a.persistenceErr.Swap(nil); message != nil {
		a.lastErr = *message
	}
}

func (a *App) reportShutdownPersistence(err error) {
	if err != nil {
		a.ReportPersistenceError(err)
	}
}
