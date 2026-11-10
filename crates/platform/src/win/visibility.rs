use super::MainThread;
use crate::Visibility;
use std::collections::BTreeMap;
use wallpaper_core::{
    displays::{DisplayId, DisplayInfo},
    policy::{Rect, contains, covered_fraction, fully_covered},
};
use windows::{
    Win32::{
        Foundation::*,
        Graphics::{Dwm::*, Gdi::*},
        UI::WindowsAndMessaging::*,
    },
    core::BOOL,
};
fn rect(r: RECT) -> Rect {
    Rect {
        x: r.left.into(),
        y: r.top.into(),
        width: i64::from(r.right) - i64::from(r.left),
        height: i64::from(r.bottom) - i64::from(r.top),
    }
}
// A disappearing or unqueryable candidate makes the sample Unknown; it must
// never preserve a previously observed Yes.
fn candidate(hwnd: HWND) -> Result<Option<Rect>, ()> {
    unsafe {
        if !IsWindowVisible(hwnd).as_bool() || IsIconic(hwnd).as_bool() {
            return Ok(None);
        }
        let mut pid = 0;
        if GetWindowThreadProcessId(hwnd, Some(&mut pid)) == 0 {
            return Err(());
        }
        if pid == std::process::id() {
            return Ok(None);
        }
        let style = GetWindowLongPtrW(hwnd, GWL_EXSTYLE) as u32;
        if style & (WS_EX_TOOLWINDOW.0 | WS_EX_LAYERED.0) != 0 {
            return Ok(None);
        }
        let mut name = [0u16; 256];
        let len = GetClassNameW(hwnd, &mut name);
        if len == 0 {
            return Err(());
        }
        if matches!(
            String::from_utf16_lossy(&name[..len as usize]).as_str(),
            "Progman" | "WorkerW" | "Shell_TrayWnd" | "Shell_SecondaryTrayWnd"
        ) {
            return Ok(None);
        }
        let mut cloaked: u32 = 0;
        DwmGetWindowAttribute(
            hwnd,
            DWMWA_CLOAKED,
            (&mut cloaked as *mut u32).cast(),
            size_of::<u32>() as u32,
        )
        .map_err(|_| ())?;
        if cloaked != 0 {
            return Ok(None);
        }
        let mut bounds = RECT::default();
        if DwmGetWindowAttribute(
            hwnd,
            DWMWA_EXTENDED_FRAME_BOUNDS,
            (&mut bounds as *mut RECT).cast(),
            size_of::<RECT>() as u32,
        )
        .is_err()
        {
            GetWindowRect(hwnd, &mut bounds).map_err(|_| ())?;
        }
        let r = rect(bounds);
        Ok((r.width > 0 && r.height > 0).then_some(r))
    }
}
#[derive(Default)]
struct Sample {
    windows: Vec<Rect>,
    failed: bool,
}
unsafe extern "system" fn collect(hwnd: HWND, data: LPARAM) -> BOOL {
    let sample = unsafe { &mut *(data.0 as *mut Sample) };
    match candidate(hwnd) {
        Ok(Some(r)) => sample.windows.push(r),
        Ok(None) => {}
        Err(()) => sample.failed = true,
    }
    true.into()
}
pub fn visibility(_main: MainThread, displays: &[DisplayInfo]) -> BTreeMap<DisplayId, Visibility> {
    use windows::Win32::UI::HiDpi::*;
    // Extended DWM bounds are physical pixels. Never compare them with a
    // DPI-virtualized monitor/work rectangle if process setup was refused.
    let dpi_ok = unsafe { GetAwarenessFromDpiAwarenessContext(GetThreadDpiAwarenessContext()) }
        == DPI_AWARENESS_PER_MONITOR_AWARE;
    if !dpi_ok {
        crate::signal_health("visibility", false);
        return displays
            .iter()
            .map(|d| (d.id.clone(), Visibility::default()))
            .collect();
    }
    let mut sample = Sample::default();
    let ok = unsafe { EnumWindows(Some(collect), LPARAM(&mut sample as *mut _ as isize)) }.is_ok()
        && !sample.failed;
    let foreground = unsafe { GetForegroundWindow() };
    let foreground = if foreground.is_invalid() {
        Ok(None)
    } else {
        candidate(foreground)
    };
    let mut healthy = ok && foreground.is_ok();
    let result = displays
        .iter()
        .map(|display| {
            let mut value = Visibility::default();
            if let Ok(foreground) = foreground {
                value.fullscreen = foreground
                    .is_some_and(|w| contains(w, display.frame.into(), 2))
                    .into();
            }
            let f = display.frame;
            let bounds = RECT {
                left: f.x,
                top: f.y,
                right: f.x.saturating_add_unsigned(f.width),
                bottom: f.y.saturating_add_unsigned(f.height),
            };
            let monitor = unsafe { MonitorFromRect(&bounds, MONITOR_DEFAULTTONULL) };
            let mut info = MONITORINFO {
                cbSize: size_of::<MONITORINFO>() as u32,
                ..Default::default()
            };
            let work_ok = !monitor.is_invalid()
                && unsafe { GetMonitorInfoW(monitor, &mut info) }.as_bool()
                && info.rcMonitor == bounds;
            healthy &= work_ok;
            if ok && work_ok {
                value.covered = fully_covered(rect(info.rcWork), &sample.windows, 2).into();
                value.coverage = Some(covered_fraction(rect(info.rcWork), &sample.windows));
            }
            (display.id.clone(), value)
        })
        .collect();
    crate::signal_health("visibility", healthy);
    result
}
