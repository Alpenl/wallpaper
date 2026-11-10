mod power;
mod visibility;
pub use power::power;
pub use visibility::visibility;
mod displays;
mod image;
pub(crate) mod probe;
pub use displays::displays;
mod pipe;
mod surface;
mod worker;
use anyhow::{Context, Result, bail};
use pipe::Pipe;
use serde::{Deserialize, Serialize};
use std::{
    marker::PhantomData,
    path::Path,
    rc::Rc,
    sync::{
        OnceLock,
        atomic::{AtomicU32, Ordering},
    },
    thread::ThreadId,
    time::{Duration, Instant},
};
use surface::Surface;
use wallpaper_core::{
    Evidence,
    displays::DisplayId,
    library::{Fit, Kind, Media},
};
use windows::{
    Win32::{
        Foundation::*,
        Storage::FileSystem::*,
        System::{Console::*, JobObjects::*, Threading::*},
    },
    core::{PCWSTR, PWSTR},
};
pub use worker::worker_main;

pub fn displays_asleep(_main: MainThread) -> bool {
    // Windows surface evidence does not depend on monitor power state.
    false
}

pub fn session_locked(_main: MainThread) -> bool {
    use windows::Win32::System::RemoteDesktop::*;
    unsafe {
        let mut buffer = PWSTR::null();
        let mut bytes = 0;
        if WTSQuerySessionInformationW(
            None, // WTS_CURRENT_SERVER_HANDLE
            WTS_CURRENT_SESSION,
            WTSSessionInfoEx,
            &mut buffer,
            &mut bytes,
        )
        .is_err()
        {
            crate::signal_health("session lock", false);
            return false;
        }
        let locked = if buffer.is_null() || (bytes as usize) < size_of::<WTSINFOEXW>() {
            crate::signal_health("session lock", false);
            false
        } else {
            let info = &*buffer.0.cast::<WTSINFOEXW>();
            if info.Level != 1 {
                crate::signal_health("session lock", false);
                false
            } else {
                crate::signal_health("session lock", true);
                info.Data.WTSInfoExLevel1.SessionFlags == WTS_SESSIONSTATE_LOCK as i32
            }
        };
        WTSFreeMemory(buffer.0.cast());
        locked
    }
}

static MAIN_THREAD: OnceLock<ThreadId> = OnceLock::new();
#[derive(Clone, Copy)]
pub struct MainThread(PhantomData<Rc<()>>);
impl MainThread {
    // Called once at process entry before GPUI starts; subsequent requests must
    // match that owner. The Rc marker prevents native owners crossing threads.
    pub fn new() -> Option<Self> {
        let id = std::thread::current().id();
        (*MAIN_THREAD.get_or_init(|| {
            // Establish physical-pixel coordinates before GPUI or any HWND exists.
            use windows::Win32::UI::HiDpi::*;
            if let Err(e) =
                unsafe { SetProcessDpiAwarenessContext(DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2) }
            {
                log::warn!("win: process DPI awareness setup: {e}");
            }
            id
        }) == id)
            .then_some(Self(PhantomData))
    }
}
pub(super) struct Handle(HANDLE);
impl Handle {
    fn new(h: HANDLE) -> Result<Self> {
        if h.is_invalid() {
            Err(windows::core::Error::from_thread().into())
        } else {
            Ok(Self(h))
        }
    }
}
impl Drop for Handle {
    fn drop(&mut self) {
        if let Err(e) = unsafe { CloseHandle(self.0) } {
            log::error!("CloseHandle: {e}");
        }
    }
}
fn wide(s: impl AsRef<std::ffi::OsStr>) -> Vec<u16> {
    use std::os::windows::ffi::OsStrExt;
    s.as_ref().encode_wide().chain(Some(0)).collect()
}

#[derive(Serialize, Deserialize)]
enum Command {
    Load {
        path: String,
        kind: Kind,
        fit: Fit,
        window: usize,
        width: u32,
        height: u32,
        paused: bool,
    },
    Sample {
        paused: bool,
    },
    Stop,
}
#[derive(Serialize, Deserialize)]
struct Observation {
    evidence: Evidence,
    reason: String,
    events: Vec<String>,
}
struct Child {
    process: Handle,
    _job: Handle,
    pipe: Pipe,
    pid: u32,
    loaded: bool,
    waiting: bool,
    disconnected: Option<Instant>,
    log_control: bool,
}
static PIPE_SERIAL: AtomicU32 = AtomicU32::new(0);
impl Child {
    fn spawn() -> Result<Self> {
        let serial = PIPE_SERIAL.fetch_add(1, Ordering::Relaxed);
        let name = format!(r"\\.\pipe\wallpaper-spike-{}-{serial}", std::process::id());
        let pipe = Pipe::server(&name)?;
        unsafe {
            let job = Handle::new(CreateJobObjectW(None, None)?)?;
            let mut limits = JOBOBJECT_EXTENDED_LIMIT_INFORMATION::default();
            limits.BasicLimitInformation.LimitFlags = JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE;
            SetInformationJobObject(
                job.0,
                JobObjectExtendedLimitInformation,
                &limits as *const _ as *const _,
                size_of_val(&limits) as u32,
            )?;
            let exe = std::env::current_exe()?;
            let app = wide(&exe);
            let mut command = wide(format!("\"{}\" --mf-worker \"{}\"", exe.display(), name));
            let startup = STARTUPINFOW {
                cb: size_of::<STARTUPINFOW>() as u32,
                ..Default::default()
            };
            let mut info = PROCESS_INFORMATION::default();
            CreateProcessW(
                PCWSTR(app.as_ptr()),
                Some(PWSTR(command.as_mut_ptr())),
                None,
                None,
                false,
                CREATE_SUSPENDED | CREATE_NO_WINDOW,
                None,
                None,
                &startup,
                &mut info,
            )?;
            let process = Handle::new(info.hProcess)?;
            let thread = Handle::new(info.hThread)?;
            // Never run the child before it belongs to the kill-on-close Job.
            if let Err(e) = AssignProcessToJobObject(job.0, process.0) {
                TerminateProcess(process.0, 1)?;
                if WaitForSingleObject(process.0, 500) != WAIT_OBJECT_0 {
                    bail!("job assignment failed ({e}); suspended child termination timed out");
                }
                return Err(e.into());
            }
            if ResumeThread(thread.0) == u32::MAX {
                TerminateProcess(process.0, 1)?;
                bail!("ResumeThread: {}", windows::core::Error::from_thread());
            }
            log::info!("mf worker started pid={}", info.dwProcessId);
            Ok(Self {
                process,
                _job: job,
                pipe,
                pid: info.dwProcessId,
                loaded: false,
                waiting: false,
                disconnected: None,
                log_control: true,
            })
        }
    }
    fn exited(&self) -> Result<bool> {
        match unsafe { WaitForSingleObject(self.process.0, 0) } {
            WAIT_OBJECT_0 => Ok(true),
            WAIT_TIMEOUT => Ok(false),
            _ => Err(windows::core::Error::from_thread().into()),
        }
    }
    fn close(&mut self) -> Result<()> {
        if self.exited()? {
            return Ok(());
        }
        let stop_sent = if self.loaded {
            match self.pipe.send(&Command::Stop) {
                Ok(()) => true,
                Err(e) => {
                    log::warn!("worker stop IPC: {e:#}");
                    false
                }
            }
        } else {
            false
        };
        unsafe {
            if !stop_sent || WaitForSingleObject(self.process.0, 1500) != WAIT_OBJECT_0 {
                if stop_sent {
                    log::warn!("worker graceful exit exceeded 1500ms; terminating job member");
                }
                TerminateProcess(self.process.0, 1)?;
                if WaitForSingleObject(self.process.0, 500) != WAIT_OBJECT_0 {
                    bail!("mf: child did not exit after terminate");
                }
            }
        }
        let mut code = 0;
        unsafe {
            GetExitCodeProcess(self.process.0, &mut code)?;
        }
        if stop_sent && code != 0 {
            bail!("mf worker shutdown exit code {code}");
        }
        Ok(())
    }
}
impl Drop for Child {
    fn drop(&mut self) {
        if let Err(e) = self.close() {
            log::error!("worker close: {e:#}");
        }
    }
}
pub struct Desktop {
    child: Option<Child>,
    surface: Surface,
    media: Media,
    restart_enabled: bool,
    retries: u8,
    restarted: bool,
    paused: bool,
    next_sample: Instant,
    clock: Instant,
    control: crate::worker_control::Control,
}
impl Desktop {
    pub const BACKEND: &'static str = "mf";
    pub fn obscured(_evidence: &Evidence) -> bool {
        false
    }
    pub fn new(display: &DisplayId, media: &Media, paused: bool, main: MainThread) -> Result<Self> {
        let surface = Surface::new(display, main)?;
        let child = Child::spawn()?;
        Ok(Self {
            child: Some(child),
            surface,
            media: media.clone(),
            restart_enabled: true,
            retries: 0,
            restarted: false,
            paused,
            next_sample: Instant::now(),
            clock: Instant::now(),
            control: crate::worker_control::Control::default(),
        })
    }
    pub fn reposition(&mut self, main: MainThread) -> Result<()> {
        self.surface.reposition(main)
    }
    pub fn set_restart_enabled(&mut self, enabled: bool) {
        self.restart_enabled = enabled;
    }
    pub fn take_restarted(&mut self) -> bool {
        std::mem::take(&mut self.restarted)
    }
    pub fn confirmed(&mut self) {
        self.retries = 0;
    }
    pub fn set_paused(&mut self, paused: bool, _main: MainThread) -> Result<()> {
        if self.media.kind == Kind::Image {
            return Ok(());
        }
        if self.paused != paused {
            log::info!("worker control target paused={paused}");
        }
        self.paused = paused;
        self.control.target(paused);
        Ok(())
    }
    pub fn after_gap(&mut self) {
        self.control.after_gap(self.clock.elapsed());
    }
    fn timeout(&self) -> Duration {
        Duration::from_secs(if self.media.kind == Kind::Image {
            15
        } else {
            3
        })
    }
    pub fn poll(&mut self, _main: MainThread, paused: bool) -> Result<Option<Evidence>> {
        if !self.surface.alive() {
            bail!("win: desktop surface lost");
        }
        self.paused = paused;
        if self.child.as_ref().context("mf: closed")?.exited()? {
            let pid = self.child.as_ref().unwrap().pid;
            let mut code = 0;
            unsafe {
                GetExitCodeProcess(self.child.as_ref().unwrap().process.0, &mut code)?;
            }
            log::warn!(
                "mf worker exited pid={pid} code={code}; consecutive restarts={}",
                self.retries
            );
            self.child.take();
            self.surface.hide();
            if !self.restart_enabled {
                bail!("mf worker exited (code {code})");
            }
            if self.retries >= 3 {
                bail!("mf worker exited (code {code}); three consecutive restarts exhausted");
            }
            self.retries += 1;
            self.child = Some(Child::spawn()?);
            self.restarted = true;
            self.next_sample = Instant::now();
            return Ok(None);
        }
        let timeout = self.timeout();
        let child = self.child.as_mut().unwrap();
        if !child.pipe.connect(child.pid)? {
            return Ok(None);
        }
        if !child.loaded {
            child.pipe.send(&Command::Load {
                path: self.media.path.clone(),
                kind: self.media.kind,
                fit: self.media.fit,
                window: self.surface.hwnd.0 as usize,
                width: self.surface.width,
                height: self.surface.height,
                paused,
            })?;
            child.loaded = true;
            child.waiting = true;
            self.control.sent(self.clock.elapsed(), timeout);
        }
        let observation = match child.pipe.receive::<Observation>() {
            Ok(value) => value,
            Err(error) => {
                // Pipe EOF can precede the process handle becoming signalled.
                // Let the next foreground tick observe exit and restart; never
                // reuse stale evidence during this short drain interval.
                let since = child.disconnected.get_or_insert_with(Instant::now);
                if since.elapsed() < Duration::from_millis(500) {
                    return Ok(None);
                }
                return Err(error.context("worker IPC disconnected without process exit"));
            }
        };
        if let Some(mut observation) = observation {
            child.waiting = false;
            self.control.replied();
            if std::mem::take(&mut child.log_control) {
                log::info!("worker control result received");
            }
            for event in observation.events.iter().take(8) {
                if event.len() <= 96
                    && (event == "playback recovered"
                        || (event.starts_with("error code=")
                            && event[11..]
                                .bytes()
                                .all(|b| b.is_ascii_hexdigit() || b == b'x')))
                {
                    log::info!("worker event: {event}");
                }
            }

            self.next_sample = Instant::now() + Duration::from_secs(2);
            if !observation.reason.is_empty() {
                bail!("mf: {}", observation.reason);
            }
            observation.evidence.child_pid = child.pid;
            if observation.evidence.ready_for_display == wallpaper_core::Truth::Yes {
                self.surface.show();
            }
            self.surface.evidence(&mut observation.evidence)?;
            if let Some(paused) = self.control.pending() {
                child.log_control = true;
                child.pipe.send(&Command::Sample { paused })?;
                child.waiting = true;
                self.control.sent(self.clock.elapsed(), timeout);
            }
            return Ok(Some(observation.evidence));
        }
        if child.waiting && self.control.expired(self.clock.elapsed()) {
            bail!("mf: worker observation timeout");
        }
        if !child.waiting
            && (self.control.pending().is_some() || Instant::now() >= self.next_sample)
        {
            child.log_control = self.control.pending().is_some();
            child.pipe.send(&Command::Sample { paused })?;
            child.waiting = true;
            self.control.sent(self.clock.elapsed(), timeout);
        }
        Ok(None)
    }
    pub fn close(&mut self, _main: MainThread) -> Result<()> {
        let child = self.child.as_mut().map(Child::close).transpose();
        self.child.take();
        let surface = self.surface.close();
        match (child, surface) {
            (Err(a), Err(b)) => bail!("worker: {a:#}; surface: {b:#}"),
            (Err(e), _) | (_, Err(e)) => Err(e),
            _ => Ok(()),
        }
    }
}
pub fn replace_file(from: &Path, to: &Path) -> Result<()> {
    let from = wide(from);
    let to = wide(to);
    unsafe {
        MoveFileExW(
            PCWSTR(from.as_ptr()),
            PCWSTR(to.as_ptr()),
            MOVEFILE_REPLACE_EXISTING | MOVEFILE_WRITE_THROUGH,
        )?;
    }
    Ok(())
}
pub fn install_exit_handler() -> Result<()> {
    unsafe extern "system" fn handler(kind: u32) -> windows::core::BOOL {
        if ![
            CTRL_C_EVENT,
            CTRL_BREAK_EVENT,
            CTRL_CLOSE_EVENT,
            CTRL_LOGOFF_EVENT,
            CTRL_SHUTDOWN_EVENT,
        ]
        .contains(&kind)
        {
            return false.into();
        }
        super::EXIT_REQUESTED.store(true, Ordering::Release);
        // CLOSE forcibly ends the process after this callback returns. Give the
        // foreground loop time to close surfaces and publish the final snapshot.
        if kind != CTRL_C_EVENT && kind != CTRL_BREAK_EVENT {
            let start = Instant::now();
            while !super::EXIT_FINISHED.load(Ordering::Acquire)
                && start.elapsed() < Duration::from_millis(4500)
            {
                std::thread::sleep(Duration::from_millis(10));
            }
        }
        true.into()
    }
    unsafe {
        SetConsoleCtrlHandler(Some(handler), true)?;
    }
    Ok(())
}
