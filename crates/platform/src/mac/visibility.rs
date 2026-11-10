use super::{MainThread, displays::screens};
use crate::Visibility;
use objc2_core_foundation::{
    CFArray, CFDictionary, CFNumber, CFRetained, CFString, CFType, CGRect,
};
use objc2_core_graphics::*;
use std::collections::BTreeMap;
use wallpaper_core::{
    displays::{DisplayId, DisplayInfo},
    policy::{Rect, contains, covered_fraction, fully_covered},
};

fn windows() -> Option<Vec<Rect>> {
    let list = CGWindowListCopyWindowInfo(
        CGWindowListOption::OptionOnScreenOnly | CGWindowListOption::ExcludeDesktopElements,
        0,
    )?;
    let list = unsafe { CFRetained::cast_unchecked::<CFArray<CFType>>(list) };
    let mut out = Vec::new();
    for item in list.iter() {
        let dict = item.downcast_ref::<CFDictionary>()?;
        let dict =
            unsafe { &*(dict as *const CFDictionary).cast::<CFDictionary<CFString, CFType>>() };
        // Only geometry, layer, alpha and PID are read. Never names or titles.
        let number = |key: &objc2_core_foundation::CFString| {
            dict.get(key.as_ref())
                .and_then(|v| v.downcast_ref::<CFNumber>().and_then(CFNumber::as_f64))
        };
        let (layer, alpha, pid) = unsafe {
            (
                number(kCGWindowLayer)?,
                number(kCGWindowAlpha)?,
                number(kCGWindowOwnerPID)?,
            )
        };
        if layer != 0.0
            || !alpha.is_finite()
            || alpha < 0.99
            || pid == f64::from(std::process::id())
        {
            continue;
        }
        let bounds = dict.get(unsafe { kCGWindowBounds }.as_ref())?;
        let bounds = bounds.downcast_ref::<CFDictionary>()?;
        let mut rect = CGRect::default();
        if !unsafe { CGRectMakeWithDictionaryRepresentation(Some(bounds), &mut rect) } {
            return None;
        }
        let (x, y, w, h) = (
            rect.origin.x,
            rect.origin.y,
            rect.size.width,
            rect.size.height,
        );
        if ![x, y, w, h].iter().all(|v| v.is_finite()) {
            return None;
        }
        if w > 0.0 && h > 0.0 {
            // Round inward: fractional transparent slivers must not become coverage.
            out.push(Rect {
                x: x.ceil() as i64,
                y: y.ceil() as i64,
                width: ((x + w).floor() - x.ceil()) as i64,
                height: ((y + h).floor() - y.ceil()) as i64,
            });
        }
    }
    Some(out)
}
pub fn visibility(main: MainThread, displays: &[DisplayInfo]) -> BTreeMap<DisplayId, Visibility> {
    let windows = windows();
    let screens = screens(main).ok();
    crate::signal_health("visibility", windows.is_some() && screens.is_some());
    displays
        .iter()
        .map(|display| {
            let mut value = Visibility::default();
            if let Some(windows) = &windows {
                value.fullscreen = windows
                    .iter()
                    .any(|w| contains(*w, display.frame.into(), 2))
                    .into();
                if let Some((_, screen)) = screens
                    .as_ref()
                    .and_then(|ss| ss.iter().find(|(d, _)| d.id == display.id))
                {
                    let frame = screen.frame();
                    let visible = screen.visibleFrame();
                    // Cocoa bottom-left -> CG top-left: the top inset uses maxY.
                    let left = (visible.origin.x - frame.origin.x).max(0.0).ceil() as i64;
                    let right =
                        (frame.origin.x + frame.size.width - visible.origin.x - visible.size.width)
                            .max(0.0)
                            .ceil() as i64;
                    let top = (frame.origin.y + frame.size.height
                        - visible.origin.y
                        - visible.size.height)
                        .max(0.0)
                        .ceil() as i64;
                    let bottom = (visible.origin.y - frame.origin.y).max(0.0).ceil() as i64;
                    let full = Rect::from(display.frame);
                    let work = Rect {
                        x: full.x + left,
                        y: full.y + top,
                        width: full.width - left - right,
                        height: full.height - top - bottom,
                    };
                    value.covered = fully_covered(work, windows, 2).into();
                    value.coverage = Some(covered_fraction(work, windows));
                }
            }
            (display.id.clone(), value)
        })
        .collect()
}
