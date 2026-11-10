use anyhow::Result as AnyResult;
use sha2::{Digest, Sha256};
use std::{
    fs::{self, File, OpenOptions, TryLockError},
    io::ErrorKind,
    path::{Path, PathBuf},
};

#[cfg(windows)]
#[path = "win/instance.rs"]
mod win;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Acquire {
    Primary,
    Secondary,
    LegacyRunning,
}

fn socket_path(key: &str, uid: i64, dirs: &[PathBuf]) -> PathBuf {
    let hash = Sha256::digest(key.as_bytes());
    let hash: String = hash[..6].iter().map(|byte| format!("{byte:02x}")).collect();
    let name = format!("mygo-{uid}-{hash}.sock");
    dirs.iter()
        .filter(|dir| !dir.as_os_str().is_empty())
        .map(|dir| dir.join(&name))
        .find(|path| path.as_os_str().as_encoded_bytes().len() < 100)
        .unwrap_or_else(|| Path::new("/tmp").join(name))
}

fn legacy_candidates() -> Vec<PathBuf> {
    #[cfg(target_os = "macos")]
    let uid = {
        unsafe extern "C" {
            fn getuid() -> u32;
        }
        i64::from(unsafe { getuid() })
    };
    #[cfg(windows)]
    let uid = -1;
    let dirs = [
        std::env::var_os("XDG_RUNTIME_DIR")
            .map(PathBuf::from)
            .unwrap_or_default(),
        std::env::temp_dir(),
        PathBuf::from("/tmp"),
    ];
    ["Wallpaper", "io.github.wpd.wallpaper\nWallpaper"]
        .iter()
        .map(|key| socket_path(key, uid, &dirs))
        .collect()
}

fn legacy_running(candidates: &[PathBuf]) -> bool {
    candidates.iter().any(|path| {
        // Connecting and dropping sends EOF, never an activation payload.
        #[cfg(target_os = "macos")]
        {
            std::os::unix::net::UnixStream::connect(path).is_ok()
        }
        #[cfg(windows)]
        {
            win::connect(path)
        }
    })
}

pub fn acquire(config_dir: &Path) -> AnyResult<Acquire> {
    acquire_with_activation(config_dir, true)
}

pub fn acquire_with_activation(config_dir: &Path, activate: bool) -> AnyResult<Acquire> {
    acquire_inner(config_dir, &legacy_candidates(), activate)
}

#[cfg(test)]
fn acquire_with_candidates(config_dir: &Path, candidates: &[PathBuf]) -> AnyResult<Acquire> {
    acquire_inner(config_dir, candidates, true)
}

fn acquire_inner(config_dir: &Path, candidates: &[PathBuf], activate: bool) -> AnyResult<Acquire> {
    if legacy_running(candidates) {
        return Ok(Acquire::LegacyRunning);
    }
    fs::create_dir_all(config_dir)?;
    let file = OpenOptions::new()
        .read(true)
        .write(true)
        .create(true)
        .truncate(false)
        .open(config_dir.join("instance.lock"))?;
    match file.try_lock() {
        Ok(()) => {
            match fs::remove_file(config_dir.join("activate")) {
                Ok(()) => {}
                Err(error) if error.kind() == ErrorKind::NotFound => {}
                Err(error) => return Err(error.into()),
            }
            std::mem::forget(file);
            Ok(Acquire::Primary)
        }
        Err(TryLockError::WouldBlock) => {
            if activate {
                File::create(config_dir.join("activate"))?;
            }
            Ok(Acquire::Secondary)
        }
        Err(TryLockError::Error(error)) => Err(error.into()),
    }
}

pub fn activation_requested(config_dir: &Path) -> bool {
    fs::remove_file(config_dir.join("activate")).is_ok()
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::atomic::{AtomicU64, Ordering};
    fn temp() -> PathBuf {
        static COUNTER: AtomicU64 = AtomicU64::new(0);
        std::env::temp_dir().join(format!(
            "wp-instance-{}-{}-{}",
            std::process::id(),
            std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap()
                .as_nanos(),
            COUNTER.fetch_add(1, Ordering::Relaxed)
        ))
    }

    #[test]
    fn locks_activation_and_independent_directories() {
        let first = temp();
        let second = temp();
        fs::create_dir_all(&first).unwrap();
        fs::write(first.join("activate"), b"stale").unwrap();
        // Explicit empty candidates isolate tests from any installed Go process.
        assert_eq!(
            acquire_with_candidates(&first, &[]).unwrap(),
            Acquire::Primary
        );
        assert!(!activation_requested(&first));
        assert_eq!(
            acquire_with_candidates(&first, &[]).unwrap(),
            Acquire::Secondary
        );
        assert!(activation_requested(&first));
        assert!(!activation_requested(&first));
        assert_eq!(
            acquire_with_candidates(&second, &[]).unwrap(),
            Acquire::Primary
        );
        // Primary files intentionally stay locked until process exit (also on Windows).
    }

    #[test]
    fn socket_directory_order_and_length() {
        let dirs = [
            PathBuf::new(),
            PathBuf::from("x".repeat(100)),
            PathBuf::from("/tmp"),
        ];
        let path = socket_path("Wallpaper", -1, &dirs);
        assert!(path.starts_with("/tmp"));
        assert!(
            path.file_name()
                .unwrap()
                .to_str()
                .unwrap()
                .starts_with("mygo--1-")
        );
        assert_ne!(
            path,
            socket_path("io.github.wpd.wallpaper\nWallpaper", -1, &dirs)
        );
        assert!(
            socket_path(
                "Wallpaper",
                42,
                &[PathBuf::from("/runtime"), PathBuf::from("/tmp")]
            )
            .starts_with("/runtime")
        );
    }

    #[cfg(target_os = "macos")]
    #[test]
    fn legacy_probe_connects_without_sending_or_deleting() {
        use std::{io::Read, os::unix::net::UnixListener};
        let dir = temp();
        fs::create_dir_all(&dir).unwrap();
        let socket = dir.join("old.sock");
        let listener = UnixListener::bind(&socket).unwrap();
        let config = dir.join("config");
        assert_eq!(
            acquire_with_candidates(&config, &[dir.join("missing"), socket.clone()]).unwrap(),
            Acquire::LegacyRunning
        );
        assert!(!config.exists());
        let (mut stream, _) = listener.accept().unwrap();
        let mut bytes = Vec::new();
        stream.read_to_end(&mut bytes).unwrap();
        assert!(bytes.is_empty());
        assert!(socket.exists());
        drop(listener);
        assert!(!legacy_running(std::slice::from_ref(&socket)));
        assert!(socket.exists());
        let regular = dir.join("regular");
        fs::write(&regular, b"preserve").unwrap();
        assert!(!legacy_running(std::slice::from_ref(&regular)));
        assert_eq!(fs::read(&regular).unwrap(), b"preserve");
        fs::remove_dir_all(dir).unwrap();
    }
}
