// Package web hosts untrusted pages only in a dedicated child process.
package web

import (
	"encoding/json"
	"fmt"
	"image"
	"net/netip"
	"net/url"
	"regexp"
	"strings"

	"wpapp/internal/desktop"
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

var blockedSSRFPrefixes = []netip.Prefix{
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("::/128"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
}

func normalizeHost(host string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
}

func isBlockedIP(addr netip.Addr) bool {
	addr = addr.Unmap()
	if addr.IsLoopback() || addr.IsUnspecified() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsPrivate() || addr.IsMulticast() {
		return true
	}
	for _, pfx := range blockedSSRFPrefixes {
		if pfx.Contains(addr) {
			return true
		}
	}
	return false
}

// isBlockedSSRFHost rejects localhost, loopback, link-local, RFC1918, CGNAT,
// unique-local, IPv4-mapped IPv6, and non-canonical numeric IP literals.
func isBlockedSSRFHost(host string) bool {
	h := normalizeHost(host)
	if h == "" || h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	// Strip optional IPv6 zone identifier (e.g. fe80::1%en0).
	if i := strings.IndexByte(h, '%'); i >= 0 {
		h = h[:i]
	}
	if addr, err := netip.ParseAddr(h); err == nil {
		return isBlockedIP(addr)
	}
	// Reject non-canonical IPv4 numeric forms (e.g. 2130706433, 127.1, 0177.0.0.1, 0x7f000001).
	last := h
	if i := strings.LastIndexByte(h, '.'); i >= 0 {
		last = h[i+1:]
	}
	if isNumericLabel(last) {
		return true
	}
	return false
}

func isNumericLabel(s string) bool {
	if s == "" {
		return false
	}
	if strings.HasPrefix(s, "0x") {
		if len(s) == 2 {
			return false
		}
		for i := 2; i < len(s); i++ {
			c := s[i]
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
				return false
			}
		}
		return true
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func normalizeAllowedHosts(hosts []string) []string {
	if len(hosts) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(hosts))
	out := make([]string, 0, len(hosts))
	for _, raw := range hosts {
		h := normalizeHost(raw)
		if h == "" || seen[h] || isBlockedSSRFHost(h) {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	return out
}

// AllowRequest never permits local files, custom protocols, credentials, SSRF
// targets, or hosts outside networkAllow even with network consent.
func AllowRequest(origin, target string, network bool, allowedHosts []string) bool {
	if sameOrigin(origin, target) {
		return true
	}
	if !network {
		return false
	}
	u, e := url.Parse(target)
	if e != nil || u.User != nil || u.Host == "" || u.Opaque != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	host := normalizeHost(u.Hostname())
	if isBlockedSSRFHost(host) {
		return false
	}
	for _, allowed := range allowedHosts {
		a := normalizeHost(allowed)
		if a != "" && !isBlockedSSRFHost(a) && host == a {
			return true
		}
	}
	return false
}

func AllowNavigation(origin, target string, newWindow bool) bool {
	return !newWindow && sameOrigin(origin, target)
}

// contentRuleListJSON builds the WKContentRuleList JSON for macOS. It always
// blocks all non-local http(s) and ws(s) requests by default and, only when
// network consent is granted, exempts exact normalized non-SSRF hosts.
func contentRuleListJSON(network bool, allowedHosts []string) string {
	type trigger struct {
		URLFilter string `json:"url-filter"`
	}
	type action struct {
		Type string `json:"type"`
	}
	type rule struct {
		Trigger trigger `json:"trigger"`
		Action  action  `json:"action"`
	}
	rules := []rule{
		{Trigger: trigger{URLFilter: "^https?://"}, Action: action{Type: "block"}},
		{Trigger: trigger{URLFilter: "^wss?://"}, Action: action{Type: "block"}},
	}
	if network {
		for _, h := range normalizeAllowedHosts(allowedHosts) {
			escaped := regexp.QuoteMeta(h)
			// WebKit content blocker url-filter does not support '|' alternation;
			// emit two exact-host rules covering path ('/') and bare origin ('$').
			rules = append(rules,
				rule{Trigger: trigger{URLFilter: "^https?://" + escaped + "(:[0-9]+)?/"}, Action: action{Type: "ignore-previous-rules"}},
				rule{Trigger: trigger{URLFilter: "^https?://" + escaped + "(:[0-9]+)?$"}, Action: action{Type: "ignore-previous-rules"}},
			)
		}
	}
	b, _ := json.Marshal(rules)
	return string(b)
}

func browserArgs(network bool) string {
	if network {
		return "--force-webrtc-ip-handling-policy=disable_non_proxied_udp"
	}
	return `--host-resolver-rules="MAP * ~NOTFOUND" --force-webrtc-ip-handling-policy=disable_non_proxied_udp`
}

// windowAndViewRects computes the clipping NSWindow rectangle and inner view
// frame for macOS, supporting both single-display and spanning viewports.
func windowAndViewRects(canvas, viewport image.Rectangle) (winRect image.Rectangle, viewX, viewY, viewW, viewH float64, err error) {
	if !viewport.Empty() && viewport != canvas {
		x, y, w, h, e := desktop.CanvasFrame(canvas, viewport, float64(viewport.Dx()), float64(viewport.Dy()), false)
		if e != nil {
			return image.Rectangle{}, 0, 0, 0, 0, e
		}
		return viewport, x, y, w, h, nil
	}
	return canvas, 0, 0, float64(canvas.Dx()), float64(canvas.Dy()), nil
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
// Native requestAnimationFrame is scheduled on demand only when q.size > 0 and
// fps > 0 so idle or paused pages do not spin on VSync.
const Bootstrap = `(()=>{const deny=['RTCPeerConnection','webkitRTCPeerConnection','mozRTCPeerConnection','RTCDataChannel','WebTransport'];if(!__WPK_NETWORK__)deny.push('WebSocket','EventSource');for(const k of deny){try{Object.defineProperty(window,k,{value:undefined,writable:false,configurable:false})}catch(e){}}const token=__WPK_TOKEN__;const raf=window.requestAnimationFrame.bind(window);let fps=__WPK_INITIAL_FPS__,last=0,next=0,q=new Map(),current=null,scheduled=false;function schedule(){if(!scheduled&&q.size>0&&fps>0){scheduled=true;raf(tick)}}Object.defineProperty(window,'__wpkFPS',{value:(n,key)=>{if(key===token){if(n!==undefined){fps=n;schedule()}return fps}},writable:false,configurable:false});Object.defineProperty(window,'requestAnimationFrame',{value:cb=>{const id=++next;q.set(id,cb);schedule();return id},writable:false,configurable:false});Object.defineProperty(window,'cancelAnimationFrame',{value:id=>{q.delete(id);if(current)current.delete(id)},writable:false,configurable:false});try{if(window!==top){let inherited=top.__wpkFPS(undefined,token);if(typeof inherited==='number')fps=inherited}}catch(e){}function tick(t){scheduled=false;if(fps<=0||q.size===0)return;if(t-last<1000/fps){schedule();return}last=t;const batch=q;current=batch;q=new Map();batch.forEach((cb,id)=>{batch.delete(id);try{cb(t)}catch(e){setTimeout(()=>{throw e})}});current=null;schedule()}})();`

func EffectiveFPS(manifest, profile int) int {
	if manifest == 0 {
		manifest = 30
	}
	if profile > 0 && profile < manifest {
		return profile
	}
	return manifest
}

func bootstrapScript(token string, fps int, network bool) string {
	b, _ := json.Marshal(token)
	netLit := "false"
	if network {
		netLit = "true"
	}
	s := strings.Replace(Bootstrap, "__WPK_NETWORK__", netLit, 1)
	s = strings.Replace(s, "__WPK_TOKEN__", string(b), 1)
	return strings.Replace(s, "__WPK_INITIAL_FPS__", fmt.Sprint(fps), 1)
}

func authorizedFPS(fps int, token string) string {
	b, _ := json.Marshal(token)
	return fmt.Sprintf("(function apply(w){try{w.__wpkFPS(%d,%s);for(let i=0;i<w.frames.length;i++)apply(w.frames[i])}catch(e){}})(window);", fps, b)
}
