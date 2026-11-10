use super::MainThread;
use crate::display_id::{clean_name, mac_id};
use anyhow::{Context, Result};
use objc2::{msg_send, rc::Retained};
use objc2_app_kit::NSScreen;
use objc2_core_foundation::{CFRetained, CFUUID};
use objc2_core_graphics::CGDisplayBounds;
use objc2_foundation::NSString;
use std::ptr::NonNull;
use wallpaper_core::displays::{DisplayInfo, Frame, dedupe};

#[link(name = "ColorSync", kind = "framework")]
unsafe extern "C" {
    fn CGDisplayCreateUUIDFromDisplayID(display: u32) -> *mut CFUUID;
}

pub(super) fn screens(main: MainThread) -> Result<Vec<(DisplayInfo, Retained<NSScreen>)>> {
    let mut out = Vec::new();
    for (index, screen) in NSScreen::screens(main).iter().enumerate() {
        let number = screen
            .deviceDescription()
            .objectForKey(&NSString::from_str("NSScreenNumber"))
            .context("mac: screen number missing")?;
        let number: u32 = unsafe { msg_send![&*number, unsignedIntValue] };
        // Both Create results own +1 references. CFRetained releases them on all paths.
        let uuid = NonNull::new(unsafe { CGDisplayCreateUUIDFromDisplayID(number) })
            .map(|ptr| unsafe { CFRetained::from_raw(ptr) });
        let uuid = uuid
            .as_ref()
            .and_then(|u| CFUUID::new_string(None, Some(u)))
            .map(|s| s.to_string());
        let bounds = CGDisplayBounds(number);
        out.push((
            DisplayInfo {
                id: mac_id(uuid.as_deref(), number),
                legacy_ids: vec![],
                name: clean_name(&screen.localizedName().to_string()),
                primary: index == 0,
                frame: Frame {
                    x: bounds.origin.x as i32,
                    y: bounds.origin.y as i32,
                    width: bounds.size.width as u32,
                    height: bounds.size.height as u32,
                },
                scale: screen.backingScaleFactor(),
            },
            screen,
        ));
    }
    out.sort_by_key(|(d, _)| (d.frame.x, d.frame.y));
    let mut infos: Vec<_> = out.iter().map(|(d, _)| d.clone()).collect();
    dedupe(&mut infos);
    for ((info, _), deduped) in out.iter_mut().zip(infos) {
        *info = deduped;
    }
    Ok(out)
}
pub fn displays(main: MainThread) -> Result<Vec<DisplayInfo>> {
    Ok(screens(main)?.into_iter().map(|(info, _)| info).collect())
}
