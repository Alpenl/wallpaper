pub mod media;
pub mod startup;
use anyhow::Result as AnyResult;
use std::sync::atomic::{AtomicBool, Ordering};

pub mod instance;
pub use instance::{Acquire, acquire, activation_requested};

pub mod display_id;
mod fs;
pub use fs::{rename_new, write_atomic};

#[cfg(target_os = "macos")]
mod mac;
#[cfg(target_os = "macos")]
pub use mac::{Desktop, MainThread, displays, displays_asleep, power, session_locked, visibility};
#[cfg(windows)]
mod win;
#[cfg(windows)]
pub use win::{
    Desktop, MainThread, displays, displays_asleep, power, session_locked, visibility, worker_main,
};

static EXIT_REQUESTED: AtomicBool = AtomicBool::new(false);
static EXIT_FINISHED: AtomicBool = AtomicBool::new(false);
pub fn exit_requested() -> bool {
    EXIT_REQUESTED.load(Ordering::Acquire)
}
pub fn exit_finished() {
    EXIT_FINISHED.store(true, Ordering::Release);
}

#[cfg(target_os = "macos")]
pub fn install_exit_handler() -> AnyResult<()> {
    extern "C" fn handler(_: i32) {
        EXIT_REQUESTED.store(true, Ordering::Release);
    }
    unsafe extern "C" {
        fn signal(signal: i32, handler: usize) -> usize;
    }
    for sig in [2, 15] {
        if unsafe { signal(sig, handler as *const () as usize) } == usize::MAX {
            anyhow::bail!(
                "install signal handler: {}",
                std::io::Error::last_os_error()
            );
        }
    }
    Ok(())
}
#[cfg(windows)]
pub fn install_exit_handler() -> AnyResult<()> {
    win::install_exit_handler()
}

#[derive(Debug, Default, Clone, Copy, serde::Serialize)]
pub struct Visibility {
    pub fullscreen: wallpaper_core::Truth,
    pub covered: wallpaper_core::Truth,
    pub coverage: Option<f64>,
}
// Log failure/recovery edges only, not every polling tick.
fn signal_health(name: &'static str, healthy: bool) {
    thread_local! { static HEALTH: std::cell::RefCell<std::collections::BTreeMap<&'static str, bool>> = std::cell::RefCell::default(); }
    HEALTH.with_borrow_mut(|states| {
        let previous = states.insert(name, healthy);
        if previous != Some(healthy) {
            if !healthy {
                log::warn!("{name} query unavailable");
            } else if previous.is_some() {
                log::info!("{name} query recovered");
            }
        }
    });
}

#[cfg(any(windows, test))]
mod worker_control;
