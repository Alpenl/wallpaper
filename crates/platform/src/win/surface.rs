use super::MainThread;
use anyhow::{Context, Result, bail};
use wallpaper_core::{Evidence, Truth, displays::DisplayId};
use windows::{
    Win32::{
        Foundation::*, Graphics::Gdi::*, System::LibraryLoader::GetModuleHandleW,
        UI::WindowsAndMessaging::*,
    },
    core::w,
};

const CLASS: windows::core::PCWSTR = w!("WallpaperRustSpikeSurface");
pub struct Surface {
    pub hwnd: HWND,
    parent: HWND,
    display: DisplayId,
    position: POINT,
    worker: HWND,
    raised: bool,
    pub width: u32,
    pub height: u32,
    closed: bool,
    _main: MainThread,
}
unsafe extern "system" fn window_proc(hwnd: HWND, message: u32, w: WPARAM, l: LPARAM) -> LRESULT {
    if message == WM_NCHITTEST {
        return LRESULT(HTTRANSPARENT as isize);
    }
    unsafe { DefWindowProcW(hwnd, message, w, l) }
}
unsafe extern "system" fn find_worker(top: HWND, data: LPARAM) -> windows::core::BOOL {
    unsafe {
        if let Ok(icons) = FindWindowExW(Some(top), None, w!("SHELLDLL_DefView"), None)
            && let Ok(worker) = FindWindowExW(None, Some(top), w!("WorkerW"), None)
        {
            *(data.0 as *mut (HWND, HWND)) = (worker, icons);
        }
    }
    true.into()
}
impl Surface {
    pub fn alive(&self) -> bool {
        !self.closed
            && [self.hwnd, self.worker, self.parent]
                .into_iter()
                .all(|hwnd| unsafe { IsWindow(Some(hwnd)).as_bool() })
    }
    pub fn new(display: &DisplayId, main: MainThread) -> Result<Self> {
        unsafe {
            let instance = GetModuleHandleW(None)?;
            let class = WNDCLASSW {
                lpfnWndProc: Some(window_proc),
                hInstance: instance.into(),
                lpszClassName: CLASS,
                ..Default::default()
            };
            if RegisterClassW(&class) == 0 && GetLastError() != ERROR_CLASS_ALREADY_EXISTS {
                return Err(windows::core::Error::from_thread().into());
            }
            let progman = FindWindowW(w!("Progman"), None)?;
            if SendMessageTimeoutW(
                progman,
                0x052c,
                WPARAM(0xd),
                LPARAM(1),
                SMTO_ABORTIFHUNG,
                1000,
                None,
            )
            .0 == 0
            {
                bail!(
                    "win: could not create WorkerW: {}",
                    windows::core::Error::from_thread()
                );
            }
            let raised =
                GetWindowLongPtrW(progman, GWL_EXSTYLE) as u32 & WS_EX_NOREDIRECTIONBITMAP.0 != 0;
            let (parent, worker, icons) = if raised {
                (
                    progman,
                    FindWindowExW(Some(progman), None, w!("WorkerW"), None)?,
                    FindWindowExW(Some(progman), None, w!("SHELLDLL_DefView"), None)?,
                )
            } else {
                let mut found = (HWND::default(), HWND::default());
                EnumWindows(Some(find_worker), LPARAM(&mut found as *mut _ as isize))?;
                if found.0.is_invalid() || found.1.is_invalid() {
                    bail!("win: desktop WorkerW or icon view unavailable");
                }
                (found.0, found.0, found.1)
            };
            let info = super::displays(main)?
                .into_iter()
                .find(|d| &d.id == display)
                .context("win: display not found")?;
            let width = info.frame.width;
            let height = info.frame.height;
            let mut points = [POINT {
                x: info.frame.x,
                y: info.frame.y,
            }];
            SetLastError(ERROR_SUCCESS);
            if MapWindowPoints(None, Some(parent), &mut points) == 0
                && GetLastError() != ERROR_SUCCESS
            {
                return Err(windows::core::Error::from_thread().into());
            }
            let mut ex = WS_EX_NOACTIVATE | WS_EX_TOOLWINDOW;
            if raised {
                ex |= WS_EX_LAYERED;
            }
            let hwnd = CreateWindowExW(
                ex,
                CLASS,
                w!("wallpaper-spike"),
                WS_CHILD | WS_CLIPCHILDREN,
                points[0].x,
                points[0].y,
                width as i32,
                height as i32,
                Some(parent),
                None,
                Some(instance.into()),
                None,
            )?;
            let surface = Self {
                hwnd,
                parent,
                display: display.clone(),
                position: points[0],
                worker,
                raised,
                width,
                height,
                closed: false,
                _main: main,
            };
            if raised {
                SetLayeredWindowAttributes(hwnd, COLORREF(0), 255, LWA_ALPHA)?;
                SetWindowPos(
                    hwnd,
                    Some(icons),
                    0,
                    0,
                    0,
                    0,
                    SWP_NOMOVE | SWP_NOSIZE | SWP_NOACTIVATE,
                )?;
                SetWindowPos(
                    worker,
                    Some(HWND_BOTTOM),
                    0,
                    0,
                    0,
                    0,
                    SWP_NOMOVE | SWP_NOSIZE | SWP_NOACTIVATE,
                )?;
            }
            Ok(surface)
        }
    }
    pub fn reposition(&mut self, main: MainThread) -> Result<()> {
        let Some(info) = super::displays(main)?
            .into_iter()
            .find(|d| d.id == self.display)
        else {
            return Ok(());
        };
        let mut points = [POINT {
            x: info.frame.x,
            y: info.frame.y,
        }];
        unsafe {
            SetLastError(ERROR_SUCCESS);
            if MapWindowPoints(None, Some(self.parent), &mut points) == 0
                && GetLastError() != ERROR_SUCCESS
            {
                return Err(windows::core::Error::from_thread().into());
            }
            if self.position != points[0] {
                SetWindowPos(
                    self.hwnd,
                    None,
                    points[0].x,
                    points[0].y,
                    0,
                    0,
                    SWP_NOSIZE | SWP_NOZORDER | SWP_NOACTIVATE,
                )?;
                self.position = points[0];
            }
        }
        Ok(())
    }
    pub fn evidence(&self, e: &mut Evidence) -> Result<()> {
        unsafe {
            let attached = !self.closed
                && IsWindow(Some(self.hwnd)).as_bool()
                && IsWindow(Some(self.worker)).as_bool()
                && GetParent(self.hwnd)? == self.parent
                && class_name(self.worker) == "WorkerW"
                && class_name(self.hwnd) == "WallpaperRustSpikeSurface"
                && (!self.raised
                    || (GetParent(self.worker)? == self.parent
                        && class_name(self.parent) == "Progman"));
            e.window = self.hwnd.0 as usize;
            e.parent = self.parent.0 as usize;
            e.attached = attached.into();
            e.visible = (attached && IsWindowVisible(self.hwnd).as_bool()).into();
            e.on_screen = (attached
                && !MonitorFromWindow(self.hwnd, MONITOR_DEFAULTTONULL).is_invalid())
            .into();
            e.occluded = Truth::Unknown;
            Ok(())
        }
    }
    pub fn show(&self) {
        unsafe {
            let _ = ShowWindow(self.hwnd, SW_SHOWNA);
        }
    }
    pub fn hide(&self) {
        unsafe {
            let _ = ShowWindow(self.hwnd, SW_HIDE);
        }
    }
    pub fn close(&mut self) -> Result<()> {
        if self.closed {
            return Ok(());
        }
        unsafe {
            if IsWindow(Some(self.hwnd)).as_bool() {
                let mut pid = 0;
                GetWindowThreadProcessId(self.hwnd, Some(&mut pid));
                if pid == std::process::id() && class_name(self.hwnd) == "WallpaperRustSpikeSurface"
                {
                    DestroyWindow(self.hwnd)?;
                    for hwnd in [self.worker, self.parent] {
                        if !RedrawWindow(
                            Some(hwnd),
                            None,
                            None,
                            RDW_INVALIDATE | RDW_ERASE | RDW_ALLCHILDREN | RDW_UPDATENOW,
                        )
                        .as_bool()
                        {
                            log::warn!("desktop redraw: {}", windows::core::Error::from_thread());
                        }
                    }
                }
            }
        }
        self.closed = true;
        Ok(())
    }
}
impl Drop for Surface {
    fn drop(&mut self) {
        if let Err(e) = self.close() {
            log::error!("surface close: {e:#}");
        }
    }
}
fn class_name(hwnd: HWND) -> String {
    let mut buf = [0u16; 128];
    let n = unsafe { GetClassNameW(hwnd, &mut buf) };
    String::from_utf16_lossy(&buf[..n as usize])
}
