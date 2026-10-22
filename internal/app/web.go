package app

import (
	"context"
	"encoding/json"

	"wpapp/internal/wpk"
)

type NetworkConsent struct {
	Title string
	Hosts []string
}

func (a *App) RequestNetworkConsent(ctx context.Context, title string, hosts []string) bool {
	result := make(chan bool, 1)
	a.Do(func() {
		a.networkConsent = &NetworkConsent{title, append([]string(nil), hosts...)}
		a.networkAnswer = result
	})
	defer a.Do(func() {
		if a.networkAnswer == result {
			a.networkConsent = nil
			a.networkAnswer = nil
		}
	})
	select {
	case yes := <-result:
		return yes
	case <-ctx.Done():
		return false
	}
}
func (a *App) ResolveNetworkConsent(allow bool) {
	a.Do(func() {
		if a.networkAnswer != nil {
			select {
			case a.networkAnswer <- allow:
			default:
			}
			a.networkConsent = nil
			a.networkAnswer = nil
		}
	})
}

// Revocation replaces the isolated host, dropping in-flight requests and stores.
func (a *App) RevokeNetwork(id string) {
	a.Do(func() {
		it := a.d.Store.Library.Find(id)
		if it == nil {
			return
		}
		it.NetworkGranted = false
		a.saveLibrary()
		for _, s := range a.sortedScreens() {
			if s.item != nil && s.item.ID == id {
				s.item.NetworkGranted = false
				a.restartPlayback(s)
			}
		}
	})
}
func canonicalProperties(defs []wpk.Prop, raw string) (string, error) {
	v, e := wpk.PropertyValues(defs, raw)
	if e != nil {
		return "", e
	}
	b, e := json.Marshal(v)
	return string(b), e
}

// PreparePackageReplacement drains active renderers before a package directory
// is replaced. A new package must never inherit an already-running page's grant.
func (a *App) PreparePackageReplacement(ctx context.Context, id string) error {
	queued := make(chan []<-chan struct{}, 1)
	a.Do(func() {
		var waits []<-chan struct{}
		for _, s := range a.sortedScreens() {
			if s.item != nil && s.item.ID == id {
				a.closeScreen(s, false)
				done := make(chan struct{})
				s.ops <- func() { close(done) }
				waits = append(waits, done)
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
			}
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
