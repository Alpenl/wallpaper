//! V2 login registration. Selection is immutable and shared by reads and writes.
use anyhow::{Context, Result, ensure};
use std::{
    fs,
    io::ErrorKind,
    path::{Path, PathBuf},
};

pub const LABEL: &str = "io.github.wpd.wallpaper.v2";
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Os {
    Mac,
    Windows,
}
pub struct Backend {
    isolated: bool,
    os: Os,
    file: Option<PathBuf>,
}
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Registration {
    pub kind: u32,
    pub bytes: Vec<u8>,
}
pub struct Status {
    pub registered: bool,
    pub path_matches: Option<bool>,
    pub system_state: &'static str,
}
impl Backend {
    pub fn from_env() -> Result<Self> {
        let os = if cfg!(windows) { Os::Windows } else { Os::Mac };
        if let Some(home) = std::env::var_os("WPAPP_HOME") {
            ensure!(!home.is_empty(), "WPAPP_HOME must not be empty");
            return Ok(Self::isolated(os, &PathBuf::from(home)));
        }
        let file = if os == Os::Mac {
            Some(
                PathBuf::from(std::env::var_os("HOME").context("HOME missing")?)
                    .join("Library/LaunchAgents")
                    .join(format!("{LABEL}.plist")),
            )
        } else {
            None
        };
        Ok(Self {
            isolated: false,
            os,
            file,
        })
    }
    pub fn isolated(os: Os, root: &Path) -> Self {
        Self {
            isolated: true,
            os,
            file: Some(match os {
                Os::Mac => root.join("LaunchAgents").join(format!("{LABEL}.plist")),
                Os::Windows => root.join("run-key.txt"),
            }),
        }
    }
    pub fn name(&self) -> &'static str {
        if self.isolated { "isolated" } else { "native" }
    }
    pub fn read(&self) -> Result<Option<Registration>> {
        if let Some(path) = &self.file {
            return match fs::read(path) {
                Ok(bytes) => Ok(Some(Registration { kind: 1, bytes })),
                Err(e) if e.kind() == ErrorKind::NotFound => Ok(None),
                Err(e) => Err(e.into()),
            };
        }
        #[cfg(windows)]
        {
            registry::read(false)
        }
        #[cfg(not(windows))]
        {
            anyhow::bail!("native Windows registration unavailable")
        }
    }
    pub fn restore(&self, value: Option<&Registration>) -> Result<()> {
        if let Some(path) = &self.file {
            return if let Some(value) = value {
                fs::create_dir_all(path.parent().context("registration parent missing")?)?;
                crate::write_atomic(path, &value.bytes)
            } else {
                match fs::remove_file(path) {
                    Ok(()) => Ok(()),
                    Err(e) if e.kind() == ErrorKind::NotFound => Ok(()),
                    Err(e) => Err(e.into()),
                }
            };
        }
        #[cfg(windows)]
        {
            registry::write(value)
        }
        #[cfg(not(windows))]
        {
            anyhow::bail!("native Windows registration unavailable")
        }
    }
    fn value(&self, exe: &Path) -> Result<Registration> {
        ensure!(exe.is_absolute(), "executable must be absolute");
        let exe = exe.to_str().context("executable must be UTF-8")?;
        let text = match self.os {
            Os::Mac => plist(exe),
            Os::Windows => format!("\"{exe}\""),
        };
        let bytes = if self.file.is_some() {
            text.into_bytes()
        } else {
            text.encode_utf16()
                .chain(Some(0))
                .flat_map(u16::to_le_bytes)
                .collect()
        };
        Ok(Registration { kind: 1, bytes })
    }
    pub fn set(&self, enabled: bool, exe: &Path) -> Result<()> {
        // Existing registrations are left intact, even after the executable moves.
        // Only status reports that mismatch; no implicit path repair.
        if enabled && self.read()?.is_some() {
            return Ok(());
        }
        self.restore(
            if enabled {
                Some(self.value(exe)?)
            } else {
                None
            }
            .as_ref(),
        )
    }
    pub fn status(&self, exe: &Path) -> Result<Status> {
        let value = self.read()?;
        let path_matches = value.as_ref().map(|v| {
            if self.os == Os::Windows {
                self.value(exe)
                    .is_ok_and(|expected| v.kind == expected.kind && v.bytes == expected.bytes)
            } else {
                let text = String::from_utf8_lossy(&v.bytes);
                let expected = xml(&exe.to_string_lossy());
                text.split_once("<key>ProgramArguments</key>")
                    .and_then(|(_, rest)| rest.split_once("<array>"))
                    .and_then(|(_, rest)| rest.split_once("</array>"))
                    .is_some_and(|(args, _)| args.trim() == format!("<string>{expected}</string>"))
            }
        });
        let mut status = Status {
            registered: value.is_some(),
            path_matches,
            system_state: "unknown",
        };
        if !self.isolated && self.os == Os::Windows {
            #[cfg(windows)]
            {
                status.system_state = match registry::read(true)? {
                    None => "enabled",
                    Some(v) => match v.bytes.first() {
                        Some(b) if b % 2 == 0 => "enabled",
                        Some(_) => "disabled",
                        None => "unknown",
                    },
                };
            }
            #[cfg(not(windows))]
            {
                status.system_state = "unknown";
            }
        }
        Ok(status)
    }
}
fn xml(value: &str) -> String {
    let mut out = String::new();
    for c in value.chars() {
        out.push_str(match c {
            '&' => "&amp;",
            '<' => "&lt;",
            '>' => "&gt;",
            '"' => "&quot;",
            '\'' => "&apos;",
            _ => {
                out.push(c);
                continue;
            }
        });
    }
    out
}
pub fn plist(exe: &str) -> String {
    format!(
        "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n<plist version=\"1.0\"><dict><key>Label</key><string>{}</string><key>RunAtLoad</key><true/><key>ProgramArguments</key><array><string>{}</string></array></dict></plist>\n",
        xml(LABEL),
        xml(exe)
    )
}

#[cfg(windows)]
mod registry {
    use super::*;
    use windows::{
        Win32::{
            Foundation::{ERROR_FILE_NOT_FOUND, ERROR_SUCCESS},
            System::Registry::*,
        },
        core::{PCWSTR, w},
    };
    struct Key(HKEY);
    impl Drop for Key {
        fn drop(&mut self) {
            unsafe {
                let _ = RegCloseKey(self.0);
            }
        }
    }
    fn open(approved: bool, write: bool) -> Result<Option<Key>> {
        let path = if approved {
            w!("Software\\Microsoft\\Windows\\CurrentVersion\\Explorer\\StartupApproved\\Run")
        } else {
            w!("Software\\Microsoft\\Windows\\CurrentVersion\\Run")
        };
        let mut key = HKEY::default();
        let code = unsafe {
            if write {
                RegCreateKeyExW(
                    HKEY_CURRENT_USER,
                    path,
                    None,
                    None,
                    REG_OPTION_NON_VOLATILE,
                    KEY_QUERY_VALUE | KEY_SET_VALUE,
                    None,
                    &mut key,
                    None,
                )
            } else {
                RegOpenKeyExW(HKEY_CURRENT_USER, path, None, KEY_QUERY_VALUE, &mut key)
            }
        };
        if code == ERROR_FILE_NOT_FOUND {
            return Ok(None);
        }
        code.ok()?;
        Ok(Some(Key(key)))
    }
    pub fn read(approved: bool) -> Result<Option<Registration>> {
        let Some(key) = open(approved, false)? else {
            return Ok(None);
        };
        let mut kind = REG_VALUE_TYPE::default();
        let mut len = 0;
        let code = unsafe {
            RegQueryValueExW(
                key.0,
                w!("WallpaperAppV2"),
                None,
                Some(&mut kind),
                None,
                Some(&mut len),
            )
        };
        if code == ERROR_FILE_NOT_FOUND {
            return Ok(None);
        }
        code.ok()?;
        ensure!(len <= 1024 * 1024, "registration value too large");
        let mut bytes = vec![0; len as usize];
        unsafe {
            RegQueryValueExW(
                key.0,
                w!("WallpaperAppV2"),
                None,
                Some(&mut kind),
                Some(bytes.as_mut_ptr()),
                Some(&mut len),
            )
            .ok()?;
        }
        bytes.truncate(len as usize);
        Ok(Some(Registration {
            kind: kind.0,
            bytes,
        }))
    }
    pub fn write(value: Option<&Registration>) -> Result<()> {
        // Deleting an absent value must not create the Run key.
        if value.is_none() && read(false)?.is_none() {
            return Ok(());
        }
        let key = open(false, true)?.context("Run key unavailable")?;
        let code = unsafe {
            match value {
                Some(v) => RegSetValueExW(
                    key.0,
                    PCWSTR(w!("WallpaperAppV2").as_ptr()),
                    None,
                    REG_VALUE_TYPE(v.kind),
                    Some(&v.bytes),
                ),
                None => RegDeleteValueW(key.0, w!("WallpaperAppV2")),
            }
        };
        if code != ERROR_SUCCESS && code != ERROR_FILE_NOT_FOUND {
            code.ok()?;
        }
        Ok(())
    }
}
