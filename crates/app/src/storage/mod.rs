pub mod paths;
pub use paths::Paths;

use anyhow::{Context, Result};
use serde::{Serialize, de::DeserializeOwned};
use std::{
    fs,
    io::ErrorKind,
    path::{Path, PathBuf},
    time::{SystemTime, UNIX_EPOCH},
};
use wallpaper_core::{
    config::{self, Settings},
    displays::{self, Assignments},
    library::{self, Library},
};
use wallpaper_platform::write_atomic;

const FILES: [&str; 3] = ["config.json", "library.json", "assignments.json"];

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "lowercase")]
pub enum State {
    Missing,
    Loaded,
    Recovered,
    Corrupt,
    Newer,
}

#[derive(Debug)]
pub struct FileReport<T> {
    pub state: State,
    pub detail: String,
    pub value: Option<T>,
    pub preserved_path: Option<PathBuf>,
}

impl<T> FileReport<T> {
    fn new(state: State, detail: String, value: Option<T>) -> Self {
        Self {
            state,
            detail,
            value,
            preserved_path: None,
        }
    }

    fn summary(&self) -> serde_json::Value {
        serde_json::json!({"state": self.state, "detail": self.detail})
    }
}

#[derive(Debug)]
pub struct Report {
    pub config: FileReport<Settings>,
    pub library: FileReport<Library>,
    pub assignments: FileReport<Assignments>,
}

impl Report {
    pub fn has_errors(&self) -> bool {
        [
            self.config.state,
            self.library.state,
            self.assignments.state,
        ]
        .iter()
        .any(|state| matches!(state, State::Corrupt | State::Newer))
    }

    pub fn summary(&self) -> serde_json::Value {
        serde_json::json!({
            "config.json": self.config.summary(),
            "library.json": self.library.summary(),
            "assignments.json": self.assignments.summary(),
        })
    }
}

pub struct Store;

impl Store {
    /// Read-only startup: recovered backup values stay in memory.
    pub fn inspect(paths: &Paths) -> Report {
        Self::read(paths, false)
    }

    pub fn open(paths: &Paths) -> Report {
        Self::read(paths, true)
    }

    fn read(paths: &Paths, recover: bool) -> Report {
        let mut config = load::<Settings>(
            &paths.config.join(FILES[0]),
            config::SCHEMA_VERSION,
            recover,
        );
        if let Some(settings) = &mut config.value {
            settings.normalize();
        }
        Report {
            config,
            library: load(
                &paths.config.join(FILES[1]),
                library::SCHEMA_VERSION,
                recover,
            ),
            assignments: load(
                &paths.config.join(FILES[2]),
                displays::SCHEMA_VERSION,
                recover,
            ),
        }
    }

    pub fn save_config(paths: &Paths, value: &Settings) -> Result<()> {
        save(&paths.config.join(FILES[0]), value)
    }

    /// Preserve all other stored fields, including unknown fields and values
    /// normalized only in memory by inspect.
    pub fn save_launch_at_login(paths: &Paths, enabled: bool) -> Result<()> {
        let report = Self::inspect(paths).config;
        let name = match report.state {
            State::Loaded => Some("config.json"),
            State::Recovered => Some("config.json.bak"),
            State::Missing => None,
            State::Corrupt | State::Newer => anyhow::bail!("configuration preflight failed"),
        };
        let mut raw: serde_json::Value = match name {
            Some(name) => serde_json::from_slice(&fs::read(paths.config.join(name))?)?,
            None => serde_json::to_value(Settings::default())?,
        };
        raw["startup"]["launchAtLogin"] = serde_json::Value::Bool(enabled);
        save(&paths.config.join(FILES[0]), &raw)
    }

    pub fn save_library(paths: &Paths, value: &Library) -> Result<()> {
        save(&paths.config.join(FILES[1]), value)
    }

    pub fn save_assignments(paths: &Paths, value: &Assignments) -> Result<()> {
        save(&paths.config.join(FILES[2]), value)
    }

    pub fn reset(paths: &Paths) -> Result<Vec<PathBuf>> {
        let mut moved = Vec::new();
        for name in FILES {
            for name in [name.to_owned(), format!("{name}.bak")] {
                let path = paths.config.join(name);
                match fs::symlink_metadata(&path) {
                    Ok(_) => {
                        let preserved = preserved_path(&path, "reset")?;
                        fs::rename(&path, &preserved)
                            .with_context(|| format!("reset {}", path.display()))?;
                        moved.push(preserved);
                    }
                    Err(error) if error.kind() == ErrorKind::NotFound => {}
                    Err(error) => return Err(error.into()),
                }
            }
        }
        Ok(moved)
    }
}

enum Read<T> {
    Missing,
    Valid { value: T, bytes: Vec<u8> },
    IoError(String),
    Invalid(String),
    Newer(u64),
}

fn read<T: DeserializeOwned>(path: &Path, version: u32) -> Read<T> {
    let bytes = match fs::read(path) {
        Ok(bytes) => bytes,
        Err(error) if error.kind() == ErrorKind::NotFound => return Read::Missing,
        Err(error) => return Read::IoError(error.to_string()),
    };
    let raw: serde_json::Value = match serde_json::from_slice(&bytes) {
        Ok(raw) => raw,
        Err(error) => return Read::Invalid(error.to_string()),
    };
    match raw.get("schemaVersion").and_then(serde_json::Value::as_u64) {
        Some(found) if found > u64::from(version) => return Read::Newer(found),
        Some(found) if found >= 1 => {}
        _ => return Read::Invalid("missing or invalid schemaVersion (expected >= 1)".into()),
    }
    match serde_json::from_value(raw) {
        Ok(value) => Read::Valid { value, bytes },
        Err(error) => Read::Invalid(error.to_string()),
    }
}

fn load<T: DeserializeOwned + Default>(path: &Path, version: u32, recover: bool) -> FileReport<T> {
    let (missing, reason) = match read(path, version) {
        Read::Valid { value, .. } => {
            return FileReport::new(State::Loaded, "loaded".into(), Some(value));
        }
        Read::Newer(found) => {
            return FileReport::new(
                State::Newer,
                format!("schemaVersion {found} exceeds {version}"),
                None,
            );
        }
        Read::Missing => (true, "main file missing".into()),
        Read::IoError(reason) => {
            return FileReport::new(
                State::Corrupt,
                format!("main file read failed: {reason}"),
                None,
            );
        }
        Read::Invalid(reason) => (false, reason),
    };
    let backup = path.with_file_name(format!(
        "{}.bak",
        path.file_name().unwrap().to_string_lossy()
    ));
    match read(&backup, version) {
        Read::Missing if missing => FileReport::new(
            State::Missing,
            "using defaults; no files written".into(),
            Some(T::default()),
        ),
        Read::Valid { value, bytes } => {
            if !recover {
                return FileReport::new(
                    State::Recovered,
                    format!(
                        "{reason}; using backup {} in memory; no files written",
                        backup.display()
                    ),
                    Some(value),
                );
            }
            let mut preserved = None;
            let result = (|| -> Result<()> {
                if !missing {
                    let destination = preserved_path(path, "corrupt")?;
                    fs::rename(path, &destination)?;
                    preserved = Some(destination);
                }
                write_atomic(path, &bytes)
            })();
            let mut report = match result {
                Ok(()) => FileReport::new(
                    State::Recovered,
                    format!("{reason}; restored from {}", backup.display()),
                    Some(value),
                ),
                Err(error) => FileReport::new(
                    State::Corrupt,
                    format!("{reason}; recovery failed: {error:#}"),
                    None,
                ),
            };
            if let Some(path) = &preserved {
                report
                    .detail
                    .push_str(&format!("; preserved at {}", path.display()));
            }
            report.preserved_path = preserved;
            report
        }
        backup => {
            let backup_reason = match backup {
                Read::Missing => "missing".into(),
                Read::Invalid(reason) => reason,
                Read::IoError(reason) => format!("read failed: {reason}"),
                Read::Newer(found) => format!("schemaVersion {found} exceeds {version}"),
                Read::Valid { .. } => unreachable!(),
            };
            FileReport::new(
                State::Corrupt,
                format!("main: {reason}; backup: {backup_reason}"),
                None,
            )
        }
    }
}

fn preserved_path(path: &Path, suffix: &str) -> Result<PathBuf> {
    let mut nanos = SystemTime::now().duration_since(UNIX_EPOCH)?.as_nanos();
    loop {
        let candidate = path.with_file_name(format!(
            "{}.{suffix}-{nanos}",
            path.file_name()
                .context("file name missing")?
                .to_string_lossy()
        ));
        match fs::symlink_metadata(&candidate) {
            Err(error) if error.kind() == ErrorKind::NotFound => return Ok(candidate),
            Err(error) => return Err(error.into()),
            Ok(_) => nanos += 1,
        }
    }
}

fn save<T: Serialize>(path: &Path, value: &T) -> Result<()> {
    let mut bytes = serde_json::to_vec_pretty(value)?;
    bytes.push(b'\n');
    fs::create_dir_all(path.parent().context("file parent missing")?)?;
    match fs::read(path) {
        Ok(previous) => {
            let backup = path.with_file_name(format!(
                "{}.bak",
                path.file_name().unwrap().to_string_lossy()
            ));
            write_atomic(&backup, &previous)?;
        }
        Err(error) if error.kind() == ErrorKind::NotFound => {}
        Err(error) => return Err(error.into()),
    }
    write_atomic(path, &bytes)
}
