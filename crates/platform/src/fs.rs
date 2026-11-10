use anyhow::Result as AnyResult;
use std::{fs, io::Write, path::Path};

#[cfg(windows)]
use crate::win;

pub fn write_atomic(path: &Path, bytes: &[u8]) -> AnyResult<()> {
    let parent = path
        .parent()
        .filter(|p| !p.as_os_str().is_empty())
        .unwrap_or(Path::new("."));
    let nonce = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)?
        .as_nanos();
    let tmp = parent.join(format!(".wallpaper-tmp-{}-{nonce}.tmp", std::process::id()));
    let result = (|| -> AnyResult<()> {
        let mut file = fs::OpenOptions::new()
            .write(true)
            .create_new(true)
            .open(&tmp)?;
        file.write_all(bytes)?;
        file.sync_all()?;
        drop(file);
        #[cfg(target_os = "macos")]
        fs::rename(&tmp, path)?;
        #[cfg(windows)]
        win::replace_file(&tmp, path)?;
        Ok(())
    })();
    if tmp.exists() {
        fs::remove_file(&tmp)?;
    }
    result
}

/// Atomic rename without replacing a destination that appeared concurrently.
pub fn rename_new(from: &Path, to: &Path) -> AnyResult<()> {
    #[cfg(target_os = "macos")]
    {
        use std::os::unix::ffi::OsStrExt;
        let from = std::ffi::CString::new(from.as_os_str().as_bytes())?;
        let to = std::ffi::CString::new(to.as_os_str().as_bytes())?;
        unsafe extern "C" {
            fn renamex_np(
                from: *const std::ffi::c_char,
                to: *const std::ffi::c_char,
                flags: u32,
            ) -> i32;
        }
        if unsafe { renamex_np(from.as_ptr(), to.as_ptr(), 0x00000004) } != 0 {
            return Err(std::io::Error::last_os_error().into());
        }
    }
    #[cfg(windows)]
    {
        use std::os::windows::ffi::OsStrExt;
        use windows::{
            Win32::Storage::FileSystem::{MOVEFILE_WRITE_THROUGH, MoveFileExW},
            core::PCWSTR,
        };
        let from: Vec<_> = from.as_os_str().encode_wide().chain(Some(0)).collect();
        let to: Vec<_> = to.as_os_str().encode_wide().chain(Some(0)).collect();
        unsafe {
            MoveFileExW(
                PCWSTR(from.as_ptr()),
                PCWSTR(to.as_ptr()),
                MOVEFILE_WRITE_THROUGH,
            )?;
        }
    }
    Ok(())
}
