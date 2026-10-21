package ui

import "github.com/egoist/mygo/ui"

// 图标：24×24 线框，stroke=currentColor，本项目自绘（不引入第三方图标集）。
func icon(body string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">` + body + `</svg>`))
}

var (
	icLibrary  = icon(`<rect x="3" y="3" width="7" height="7" rx="1.5"/><rect x="14" y="3" width="7" height="7" rx="1.5"/><rect x="3" y="14" width="7" height="7" rx="1.5"/><rect x="14" y="14" width="7" height="7" rx="1.5"/>`)
	icMonitor  = icon(`<rect x="2" y="4" width="20" height="13" rx="2"/><path d="M8 21h8M12 17v4"/>`)
	icDiscover = icon(`<circle cx="12" cy="12" r="9"/><path d="M15.5 8.5l-2 5-5 2 2-5z"/>`)
	icGauge    = icon(`<path d="M12 14l4-4"/><path d="M3.3 19a10 10 0 1 1 17.4 0"/>`)
	icPause    = icon(`<rect x="6" y="4" width="4" height="16" rx="1"/><rect x="14" y="4" width="4" height="16" rx="1"/>`)
	icPlay     = icon(`<path d="M7 4l13 8-13 8z"/>`)
	icBattery  = icon(`<rect x="2" y="7" width="17" height="10" rx="2"/><path d="M22 11v2"/><path d="M6 10v4M9.5 10v4"/>`)
	icPalette  = icon(`<path d="M12 3a9 9 0 1 0 0 18c1.1 0 1.5-.8 1.5-1.5 0-1.2-1-1.6-1-2.7 0-.9.7-1.6 1.6-1.6H16a5 5 0 0 0 5-5c0-4-4-7.2-9-7.2z"/><circle cx="7.5" cy="11" r="1"/><circle cx="10" cy="7" r="1"/><circle cx="15" cy="7.5" r="1"/>`)
	icRocket   = icon(`<path d="M5 15c-1.5 1.3-2 5-2 5s3.7-.5 5-2"/><path d="M9 15l-3-3a15 15 0 0 1 12-9 15 15 0 0 1-9 12z"/><circle cx="14.5" cy="9.5" r="1.5"/>`)
	icPulse    = icon(`<path d="M3 12h4l2-6 4 12 2-6h6"/>`)
	icInfo     = icon(`<circle cx="12" cy="12" r="9"/><path d="M12 11v5M12 8h.01"/>`)
	icPlus     = icon(`<path d="M12 5v14M5 12h14"/>`)
	icTrash    = icon(`<path d="M4 7h16M10 11v6M14 11v6M6 7l1 13h10l1-13M9 7V4h6v3"/>`)
	icWand     = icon(`<path d="M15 4V2M15 10V8M11 6H9M21 6h-2M4 20L14 10"/><path d="M18 3l-1.5 1.5M18 9l-1.5-1.5"/>`)
	icFolder   = icon(`<path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/>`)
	icRefresh  = icon(`<path d="M20 11a8 8 0 0 0-14.9-3M4 4v4h4"/><path d="M4 13a8 8 0 0 0 14.9 3M20 20v-4h-4"/>`)
	icSliders  = icon(`<path d="M4 6h10M18 6h2M4 12h4M12 12h8M4 18h12M20 18h0"/><circle cx="16" cy="6" r="2"/><circle cx="10" cy="12" r="2"/><circle cx="18" cy="18" r="2"/>`)
	icSkip     = icon(`<path d="M5 4l10 8-10 8z"/><path d="M19 5v14"/>`)
	icImport   = icon(`<path d="M12 3v12M7 10l5 5 5-5"/><path d="M5 21h14"/>`)
	icGlobe    = icon(`<circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3a14 14 0 0 1 0 18M12 3a14 14 0 0 0 0 18"/>`)
	icSparkle  = icon(`<path d="M12 3l1.8 5.2L19 10l-5.2 1.8L12 17l-1.8-5.2L5 10l5.2-1.8z"/><path d="M19 17l.7 1.8 1.8.7-1.8.7L19 22l-.7-1.8-1.8-.7 1.8-.7z"/>`)
	icShield   = icon(`<path d="M12 3l8 3v6c0 4.5-3.4 8.3-8 9-4.6-.7-8-4.5-8-9V6z"/>`)
	icCheck    = icon(`<path d="M5 12l5 5 9-10"/>`)
	icVideo    = icon(`<rect x="3" y="6" width="13" height="12" rx="2"/><path d="M16 10l5-3v10l-5-3"/>`)
	icImage    = icon(`<rect x="3" y="4" width="18" height="16" rx="2"/><circle cx="9" cy="9.5" r="1.5"/><path d="M21 16l-5-5-9 9"/>`)
	icSearch   = icon(`<circle cx="11" cy="11" r="7"/><path d="M20 20l-4-4"/>`)
	icList     = icon(`<path d="M8 6h13M8 12h13M8 18h13M3 6h.01M3 12h.01M3 18h.01"/>`)
	icDownload = icon(`<path d="M12 4v11M7 10l5 5 5-5"/><path d="M4 20h16"/>`)
)
