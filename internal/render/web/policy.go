// Package web hosts untrusted pages only in a dedicated child process.
package web

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"wpapp/internal/wpk"
)

func Origin(id, platform string) string {
	if platform == "darwin" {
		return "wpk://" + id
	}
	return "https://" + id + ".wpk.invalid"
}
func sameOrigin(origin, target string) bool {
	a, e := url.Parse(origin)
	if e != nil {
		return false
	}
	b, e := url.Parse(target)
	if e != nil {
		return false
	}
	return b.User == nil && a.Scheme == b.Scheme && strings.EqualFold(a.Host, b.Host) && b.Opaque == ""
}

// AllowRequest never permits local files, custom protocols or credentials even
// with network consent. Permissions are independent of top-level navigation.
func AllowRequest(origin, target string, network bool) bool {
	if sameOrigin(origin, target) {
		return true
	}
	u, e := url.Parse(target)
	return e == nil && u.User == nil && u.Host != "" && network && (u.Scheme == "http" || u.Scheme == "https")
}
func AllowNavigation(origin, target string, newWindow bool) bool {
	return !newWindow && sameOrigin(origin, target)
}

func PropertyScript(defs []wpk.Prop, overrides string) (string, error) {
	values, err := wpk.PropertyValues(defs, overrides)
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(values)
	if err != nil {
		return "", err
	}
	// encoding/json escapes HTML and JS line separators. No property is spliced
	// into an identifier, quoted string or executable fragment.
	literal, _ := json.Marshal(string(b))
	return `(function(p){if(window.wpk&&typeof window.wpk.onProps==='function')window.wpk.onProps(p);if(typeof window.livelyPropertyListener==='function')Object.keys(p).forEach(k=>window.livelyPropertyListener(k,p[k]));if(window.wallpaperPropertyListener&&typeof window.wallpaperPropertyListener.applyUserProperties==='function'){let q=Object.create(null);Object.keys(p).forEach(k=>q[k]={value:p[k]});window.wallpaperPropertyListener.applyUserProperties(q);}})(` + "JSON.parse(" + string(literal) + ")" + `);`, nil
}

// Installed at document start in every frame, before package scripts. Updating
// the closure's cap also affects callbacks already scheduled by the page.
const Bootstrap = `(()=>{const token=__WPK_TOKEN__;const raf=window.requestAnimationFrame.bind(window);let fps=__WPK_INITIAL_FPS__,last=0,next=0,q=new Map(),current=null;Object.defineProperty(window,'__wpkFPS',{value:(n,key)=>{if(key===token){if(n!==undefined)fps=n;return fps}},writable:false,configurable:false});Object.defineProperty(window,'requestAnimationFrame',{value:cb=>{const id=++next;q.set(id,cb);return id},writable:false,configurable:false});Object.defineProperty(window,'cancelAnimationFrame',{value:id=>{q.delete(id);if(current)current.delete(id)},writable:false,configurable:false});try{if(window!==top){let inherited=top.__wpkFPS(undefined,token);if(typeof inherited==='number')fps=inherited}}catch(e){}function tick(t){raf(tick);if(fps>0&&t-last<1000/fps)return;last=t;const batch=q;current=batch;q=new Map();batch.forEach((cb,id)=>{batch.delete(id);try{cb(t)}catch(e){setTimeout(()=>{throw e})}});current=null}raf(tick);})();`

func FPSScript(fps int) (string, error) {
	if fps < 0 || fps > 240 {
		return "", fmt.Errorf("web: invalid FPS")
	}
	return fmt.Sprintf("window.__wpkFPS(%d);", fps), nil
}
func EffectiveFPS(manifest, profile int) int {
	if manifest == 0 {
		manifest = 30
	}
	if profile > 0 && profile < manifest {
		return profile
	}
	return manifest
}

func bootstrapScript(token string, fps int) string {
	b, _ := json.Marshal(token)
	return strings.Replace(strings.Replace(Bootstrap, "__WPK_TOKEN__", string(b), 1), "__WPK_INITIAL_FPS__", fmt.Sprint(fps), 1)
}
func authorizedFPS(fps int, token string) string {
	b, _ := json.Marshal(token)
	return fmt.Sprintf("(function apply(w){try{w.__wpkFPS(%d,%s);for(let i=0;i<w.frames.length;i++)apply(w.frames[i])}catch(e){}})(window);", fps, b)
}
