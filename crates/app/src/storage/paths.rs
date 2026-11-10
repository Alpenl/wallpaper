use anyhow::{Context, Result};
use std::{ffi::OsString, path::PathBuf};

#[derive(Debug, Clone)]
pub struct Paths {
    pub config: PathBuf,
    pub library: PathBuf,
    pub cache: PathBuf,
    pub logs: PathBuf,
}

impl Paths {
    pub fn from_env() -> Result<Self> {
        Self::resolve(|name| std::env::var_os(name))
    }

    fn resolve(env: impl Fn(&str) -> Option<OsString>) -> Result<Self> {
        let get = |name| {
            env(name)
                .filter(|value| !value.is_empty())
                .map(PathBuf::from)
        };
        if let Some(home) = get("WPAPP_HOME") {
            let root = home.join("v2");
            return Ok(Self {
                config: root.join("config"),
                library: root.join("library"),
                cache: root.join("cache"),
                logs: root.join("logs"),
            });
        }
        #[cfg(target_os = "macos")]
        {
            let home = get("HOME").context("HOME is not set")?;
            let root = home.join("Library/Application Support/io.github.wpd.wallpaper/v2");
            Ok(Self {
                config: root.join("config"),
                library: root.join("library"),
                cache: home.join("Library/Caches/io.github.wpd.wallpaper/v2"),
                logs: home.join("Library/Logs/io.github.wpd.wallpaper/v2"),
            })
        }
        #[cfg(windows)]
        {
            let roaming = get("APPDATA").context("APPDATA is not set")?;
            let local = get("LOCALAPPDATA")
                .context("LOCALAPPDATA is not set")?
                .join("WallpaperApp/v2");
            Ok(Self {
                config: roaming.join("WallpaperApp/v2"),
                library: local.join("library"),
                cache: local.join("cache"),
                logs: local.join("logs"),
            })
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn override_and_native_paths_without_reading_user_directories() {
        let paths =
            Paths::resolve(|name| (name == "WPAPP_HOME").then(|| OsString::from("synthetic")))
                .unwrap();
        assert_eq!(paths.config, PathBuf::from("synthetic/v2/config"));
        assert_eq!(paths.library, PathBuf::from("synthetic/v2/library"));
        assert_eq!(paths.cache, PathBuf::from("synthetic/v2/cache"));
        assert_eq!(paths.logs, PathBuf::from("synthetic/v2/logs"));
        let native = Paths::resolve(|name| match name {
            "WPAPP_HOME" => Some(OsString::new()),
            "HOME" | "APPDATA" => Some(OsString::from("synthetic-roaming")),
            "LOCALAPPDATA" => Some(OsString::from("synthetic-local")),
            _ => None,
        })
        .unwrap();
        #[cfg(target_os = "macos")]
        {
            assert_eq!(
                native.config,
                PathBuf::from(
                    "synthetic-roaming/Library/Application Support/io.github.wpd.wallpaper/v2/config"
                )
            );
            assert_eq!(
                native.library,
                native.config.parent().unwrap().join("library")
            );
            assert_eq!(
                native.cache,
                PathBuf::from("synthetic-roaming/Library/Caches/io.github.wpd.wallpaper/v2")
            );
            assert_eq!(
                native.logs,
                PathBuf::from("synthetic-roaming/Library/Logs/io.github.wpd.wallpaper/v2")
            );
        }
        #[cfg(windows)]
        {
            assert_eq!(
                native.config,
                PathBuf::from("synthetic-roaming/WallpaperApp/v2")
            );
            assert_eq!(
                native.library,
                PathBuf::from("synthetic-local/WallpaperApp/v2/library")
            );
            assert_eq!(
                native.cache,
                PathBuf::from("synthetic-local/WallpaperApp/v2/cache")
            );
            assert_eq!(
                native.logs,
                PathBuf::from("synthetic-local/WallpaperApp/v2/logs")
            );
        }
        assert!(Paths::resolve(|_| None).is_err());
    }
}
