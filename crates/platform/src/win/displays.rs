use super::{MainThread, wide};
use crate::display_id::{DcTarget, MonIdentity};
use anyhow::Result;
use std::{cell::RefCell, collections::BTreeMap};
use wallpaper_core::displays::{DisplayInfo, Frame, dedupe, sort_by_layout};
use windows::{
    Win32::{
        Devices::Display::*,
        Foundation::*,
        Graphics::Gdi::*,
        System::Com::*,
        UI::{
            HiDpi::*,
            Shell::{DesktopWallpaper, IDesktopWallpaper},
            WindowsAndMessaging::{EDD_GET_DEVICE_INTERFACE_NAME, MONITORINFOF_PRIMARY},
        },
    },
    core::{BOOL, PCWSTR},
};

fn text(value: &[u16]) -> String {
    String::from_utf16_lossy(&value[..value.iter().position(|c| *c == 0).unwrap_or(value.len())])
}
fn targets() -> BTreeMap<String, DcTarget> {
    let mut result = BTreeMap::new();
    unsafe {
        for _ in 0..3 {
            let (mut np, mut nm) = (0, 0);
            if GetDisplayConfigBufferSizes(QDC_ONLY_ACTIVE_PATHS, &mut np, &mut nm) != ERROR_SUCCESS
                || np == 0
            {
                return result;
            }
            let mut paths = vec![DISPLAYCONFIG_PATH_INFO::default(); np as usize];
            let mut modes = vec![DISPLAYCONFIG_MODE_INFO::default(); nm.max(1) as usize];
            let error = QueryDisplayConfig(
                QDC_ONLY_ACTIVE_PATHS,
                &mut np,
                paths.as_mut_ptr(),
                &mut nm,
                modes.as_mut_ptr(),
                None,
            );
            if error == ERROR_INSUFFICIENT_BUFFER {
                continue;
            }
            if error != ERROR_SUCCESS {
                return result;
            }
            for path in paths.into_iter().take(np as usize) {
                let mut source = DISPLAYCONFIG_SOURCE_DEVICE_NAME {
                    header: DISPLAYCONFIG_DEVICE_INFO_HEADER {
                        r#type: DISPLAYCONFIG_DEVICE_INFO_GET_SOURCE_NAME,
                        size: size_of::<DISPLAYCONFIG_SOURCE_DEVICE_NAME>() as u32,
                        adapterId: path.sourceInfo.adapterId,
                        id: path.sourceInfo.id,
                    },
                    ..Default::default()
                };
                if DisplayConfigGetDeviceInfo(&mut source.header) != 0 {
                    continue;
                }
                let gdi = text(&source.viewGdiDeviceName).trim().to_uppercase();
                if result.contains_key(&gdi) {
                    continue;
                }
                let mut target = DISPLAYCONFIG_TARGET_DEVICE_NAME {
                    header: DISPLAYCONFIG_DEVICE_INFO_HEADER {
                        r#type: DISPLAYCONFIG_DEVICE_INFO_GET_TARGET_NAME,
                        size: size_of::<DISPLAYCONFIG_TARGET_DEVICE_NAME>() as u32,
                        adapterId: path.targetInfo.adapterId,
                        id: path.targetInfo.id,
                    },
                    ..Default::default()
                };
                if DisplayConfigGetDeviceInfo(&mut target.header) != 0 {
                    continue;
                }
                result.insert(
                    gdi,
                    DcTarget {
                        friendly: text(&target.monitorFriendlyDeviceName),
                        path: text(&target.monitorDevicePath),
                    },
                );
            }
            break;
        }
    }
    result
}
fn monitor_device(gdi: &str) -> (String, String) {
    let name = wide(gdi);
    let mut result = (String::new(), String::new());
    for index in 0..4 {
        let mut device = DISPLAY_DEVICEW {
            cb: size_of::<DISPLAY_DEVICEW>() as u32,
            ..Default::default()
        };
        if !unsafe {
            EnumDisplayDevicesW(
                PCWSTR(name.as_ptr()),
                index,
                &mut device,
                EDD_GET_DEVICE_INTERFACE_NAME,
            )
        }
        .as_bool()
        {
            break;
        }
        let pair = (text(&device.DeviceID), text(&device.DeviceString));
        if device.StateFlags.contains(DISPLAY_DEVICE_ACTIVE) {
            return pair;
        }
        if result.0.is_empty() {
            result = pair;
        }
    }
    result
}
fn wall_paths() -> Vec<(RECT, String)> {
    let mut result = Vec::new();
    unsafe {
        // Balance successful COM initialization, including S_FALSE. An existing
        // different apartment is usable too; it must not be uninitialized here.
        let initialized = CoInitializeEx(None, COINIT_APARTMENTTHREADED).is_ok();
        {
            if let Ok(wall) =
                CoCreateInstance::<_, IDesktopWallpaper>(&DesktopWallpaper, None, CLSCTX_ALL)
                && let Ok(count) = wall.GetMonitorDevicePathCount()
            {
                for i in 0..count {
                    if let Ok(path) = wall.GetMonitorDevicePathAt(i)
                        && !path.is_null()
                    {
                        if let Ok(rect) = wall.GetMonitorRECT(PCWSTR(path.0)) {
                            result.push((rect, path.to_string().unwrap_or_default()));
                        }
                        CoTaskMemFree(Some(path.0.cast()));
                    }
                }
            }
        }
        if initialized {
            CoUninitialize();
        }
    }
    result
}
#[derive(Default)]
struct WallCache {
    rectangles: Vec<(i32, i32, i32, i32)>,
    paths: Vec<(RECT, String)>,
}
thread_local! {
    static WALL_CACHE: RefCell<WallCache> = RefCell::default();
}
fn cached_wall_paths(infos: &[(HMONITOR, MONITORINFOEXW)]) -> Vec<(RECT, String)> {
    let mut rectangles: Vec<_> = infos
        .iter()
        .map(|(_, info)| {
            let r = info.monitorInfo.rcMonitor;
            (r.left, r.top, r.right, r.bottom)
        })
        .collect();
    rectangles.sort_unstable();
    rectangles.dedup();
    WALL_CACHE.with_borrow_mut(|cache| {
        if !cache.paths.is_empty() && cache.rectangles == rectangles {
            return cache.paths.clone();
        }
        let paths = wall_paths();
        // A COM failure must not replace the last successful topology snapshot.
        if !paths.is_empty() {
            cache.rectangles = rectangles;
            cache.paths = paths.clone();
        }
        paths
    })
}
unsafe extern "system" fn collect(monitor: HMONITOR, _: HDC, _: *mut RECT, data: LPARAM) -> BOOL {
    unsafe { &mut *(data.0 as *mut Vec<HMONITOR>) }.push(monitor);
    true.into()
}
pub fn displays(_main: MainThread) -> Result<Vec<DisplayInfo>> {
    let mut monitors: Vec<HMONITOR> = Vec::new();
    unsafe {
        EnumDisplayMonitors(
            None,
            None,
            Some(collect),
            LPARAM(&mut monitors as *mut _ as isize),
        )
        .ok()?;
    }
    let mut infos = Vec::with_capacity(monitors.len());
    for monitor in monitors {
        let mut info = MONITORINFOEXW::default();
        info.monitorInfo.cbSize = size_of::<MONITORINFOEXW>() as u32;
        unsafe {
            GetMonitorInfoW(monitor, &mut info.monitorInfo).ok()?;
        }
        infos.push((monitor, info));
    }
    let mut dc = targets();
    let walls = cached_wall_paths(&infos);
    let mut result = Vec::new();
    for (monitor, info) in infos {
        let gdi = text(&info.szDevice);
        let rect = info.monitorInfo.rcMonitor;
        let (edd_path, edd_string) = monitor_device(&gdi);
        let identity = MonIdentity {
            dc: dc.remove(&gdi.trim().to_uppercase()),
            gdi,
            edd_path,
            edd_string,
            wall_path: walls
                .iter()
                .find(|(r, _)| *r == rect)
                .map(|(_, path)| path.clone())
                .unwrap_or_default(),
        };
        let (id, name, legacy_ids) = identity.resolve();
        let (mut dx, mut dy) = (0, 0);
        let scale = if unsafe { GetDpiForMonitor(monitor, MDT_EFFECTIVE_DPI, &mut dx, &mut dy) }
            .is_ok()
            && dx > 0
        {
            f64::from(dx) / 96.0
        } else {
            1.0
        };
        result.push(DisplayInfo {
            id,
            name,
            legacy_ids,
            primary: info.monitorInfo.dwFlags & MONITORINFOF_PRIMARY != 0,
            frame: Frame {
                x: rect.left,
                y: rect.top,
                width: (rect.right - rect.left) as u32,
                height: (rect.bottom - rect.top) as u32,
            },
            scale,
        });
    }
    sort_by_layout(&mut result);
    dedupe(&mut result);
    Ok(result)
}
