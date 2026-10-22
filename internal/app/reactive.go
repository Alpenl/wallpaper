package app

import (
	"context"
	"slices"
	"wpapp/internal/config"
	"wpapp/internal/reactive"
	"wpapp/internal/visibility"
)

type SignalConsent struct{ Title, Permission string }

func (a *App) RequestSignalConsent(ctx context.Context, title, permission string) bool {
	if permission != "audio" && permission != "input" {
		return false
	}
	result := make(chan bool, 1)
	a.Do(func() { a.signalConsent = &SignalConsent{title, permission}; a.signalAnswer = result })
	defer a.Do(func() {
		if a.signalAnswer == result {
			a.signalConsent = nil
			a.signalAnswer = nil
		}
	})
	select {
	case yes := <-result:
		return yes
	case <-ctx.Done():
		return false
	}
}
func (a *App) ResolveSignalConsent(allow bool) {
	a.Do(func() {
		if a.signalAnswer != nil {
			select {
			case a.signalAnswer <- allow:
			default:
			}
			a.signalAnswer = nil
			a.signalConsent = nil
		}
	})
}
func (a *App) RevokeSignal(id, permission string) {
	a.Do(func() {
		it := a.d.Store.Library.Find(id)
		if it == nil {
			return
		}
		switch permission {
		case "audio":
			it.AudioGranted = false
		case "input":
			it.InputGranted = false
		default:
			return
		}
		a.saveLibrary()
		for _, s := range a.sortedScreens() {
			if s.item != nil && s.item.ID == id {
				s.item.AudioGranted = it.AudioGranted
				s.item.InputGranted = it.InputGranted
				a.restartPlayback(s)
			}
		}
	})
}

func (a *App) signalsBlocked(s *screen) bool {
	const mask = visibility.Fullscreen | visibility.Covered | visibility.Locked | visibility.Suspend | visibility.DisplayOff
	if a.perDisp[s.d.ID]&mask != 0 {
		return true
	}
	for r, on := range a.global {
		if on && r&mask != 0 {
			return true
		}
	}
	return false
}

// Package declaration is checked again from the on-disk manifest by the host.
func signalGrant(it *config.Item, c config.Config, permission string) bool {
	g := reactive.Gate{Declared: slices.Contains(it.Permissions, permission), Visible: true}
	if permission == "audio" {
		g.Consented = it.AudioGranted
		g.Master = c.Privacy.AudioReactive
		return g.Audio()
	}
	if permission == "input" {
		g.Consented = it.InputGranted
		g.Master = c.Privacy.MouseInput
		return g.Input()
	}
	return false
}
