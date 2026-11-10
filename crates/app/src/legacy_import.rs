//! Read-only legacy input; all media writes use the normal import transaction.
use crate::{
    import::{self, Event, Job, Options, Spec, Summary},
    library_ops::{open, real_directory, remove_directory, safe_id},
    storage::{Paths, State, Store},
};
use anyhow::{Context, Result, ensure};
use std::{
    ffi::OsString,
    fs,
    path::{Component, Path, PathBuf},
    sync::{
        atomic::{AtomicBool, Ordering},
        mpsc::Sender,
    },
};
use wallpaper_core::{
    legacy::{self, Converted, LegacyError},
    library::{Item, RejectCode, Rejection},
};

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct LegacySource {
    pub config: PathBuf,
    pub library: PathBuf,
}
#[derive(Debug, Clone, Copy)]
pub enum Os {
    Mac,
    Windows,
}

/// Pure path selection: callers supply environment values, never filesystem access.
pub fn default_source(os: Os, env: impl Fn(&str) -> Option<OsString>) -> Option<LegacySource> {
    let get = |name| env(name).filter(|v| !v.is_empty()).map(PathBuf::from);
    if let Some(home) = get("WPAPP_HOME") {
        return Some(LegacySource {
            config: home.join("config"),
            library: home.join("library"),
        });
    }
    Some(match os {
        Os::Mac => {
            let config = get("HOME")?.join("Library/Application Support/io.github.wpd.wallpaper");
            LegacySource {
                library: config.join("library"),
                config,
            }
        }
        Os::Windows => LegacySource {
            config: get("APPDATA")?.join("WallpaperApp"),
            library: get("LOCALAPPDATA")?.join("WallpaperApp/library"),
        },
    })
}

/// Identifies unavailable legacy library input for CLI exit code 8.
#[derive(Debug)]
pub struct SourceError(pub String);
impl std::fmt::Display for SourceError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(&self.0)
    }
}
impl std::error::Error for SourceError {}

pub fn start(paths: Paths, helper: PathBuf, source: LegacySource) -> Job {
    import::spawn(move |cancel, send| run(paths, helper, source, cancel, send))
}
fn note(send: &Sender<Event>, message: impl Into<String>) {
    let _ = send.send(Event::Note {
        message: message.into(),
    });
}
fn read_old<T>(
    root: &Path,
    name: &str,
    convert: fn(&str) -> std::result::Result<Converted<T>, LegacyError>,
    send: &Sender<Event>,
) -> Result<T> {
    let read = |path: &Path| -> Result<Converted<T>> {
        let text = fs::read_to_string(path).with_context(|| format!("read {}", path.display()))?;
        Ok(convert(&text)?)
    };
    let path = root.join(name);
    let converted = match read(&path) {
        Ok(value) => value,
        // A newer schema is not corruption: never silently import an older backup.
        Err(e)
            if e.downcast_ref::<LegacyError>()
                .is_some_and(|e| matches!(e, LegacyError::NewerVersion { .. })) =>
        {
            return Err(e);
        }
        Err(main) => {
            let backup = root.join(format!("{name}.bak"));
            let value = read(&backup)
                .with_context(|| format!("{}: {main:#}; backup unavailable", path.display()))?;
            note(
                send,
                format!(
                    "{}: {main:#}; using read-only backup {}",
                    path.display(),
                    backup.display()
                ),
            );
            value
        }
    };
    for message in converted.notes {
        note(send, message);
    }
    Ok(converted.value)
}

fn relative_entry(entry: &str) -> Result<()> {
    // Check both separators even on Unix so Windows traversal/ADS cannot slip in.
    ensure!(
        !entry.is_empty() && !entry.contains([':', '\0']),
        "unsafe entry: {entry:?}"
    );
    let normalized = entry.replace('\\', "/");
    let path = Path::new(&normalized);
    ensure!(
        !path.is_absolute()
            && path
                .components()
                .all(|c| matches!(c, Component::Normal(_) | Component::CurDir))
            && path.components().any(|c| matches!(c, Component::Normal(_))),
        "unsafe entry: {entry:?}"
    );
    Ok(())
}
fn contained_file(root: &Path, id: &str, entry: &str) -> Result<PathBuf> {
    safe_id(id)?;
    relative_entry(entry)?;
    let entry = entry.replace('\\', "/");
    let root = fs::canonicalize(root)?;
    let directory = fs::canonicalize(root.join(id))?;
    ensure!(
        directory.starts_with(&root) && directory != root,
        "item directory escapes library: {id}"
    );
    let path = fs::canonicalize(directory.join(&entry))?;
    ensure!(
        path.starts_with(&directory) && path != directory,
        "entry escapes item directory: {entry:?}"
    );
    ensure!(
        fs::metadata(&path)?.is_file(),
        "entry is not a file: {entry:?}"
    );
    // Keep the declared extension/stem, even when the contained entry is a symlink.
    Ok(root.join(id).join(entry))
}

// Resolve existing ancestors without creating anything. Prevent a caller from
// directing cleanup/rollback into the read-only source (including via symlinks).
fn projected(path: &Path) -> Result<PathBuf> {
    let absolute = if path.is_absolute() {
        path.to_owned()
    } else {
        std::env::current_dir()?.join(path)
    };
    match fs::canonicalize(&absolute) {
        Ok(path) => Ok(path),
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => {
            let parent = projected(absolute.parent().context("path has no parent")?)?;
            Ok(parent.join(absolute.file_name().context("path has no name")?))
        }
        Err(e) => Err(e.into()),
    }
}
fn separate_destination(paths: &Paths, source: &LegacySource) -> Result<()> {
    let config = projected(&source.config)?;
    let library = projected(&source.library)?;
    for path in [&paths.config, &paths.library, &paths.cache, &paths.logs] {
        let path = projected(path)?;
        ensure!(
            !config.starts_with(&path)
                && !library.starts_with(&path)
                && !path.starts_with(&library),
            "destination overlaps read-only legacy source: {}",
            path.display()
        );
    }
    Ok(())
}

fn run(
    paths: Paths,
    helper: PathBuf,
    source: LegacySource,
    cancel: &AtomicBool,
    send: &Sender<Event>,
) -> Result<Summary> {
    separate_destination(&paths, &source)?;
    let report = open(&paths)?;
    let mut library = report.library.value.context("missing library")?;
    import::prepare(&paths)?;
    let old = read_old(
        &source.config,
        "library.json",
        legacy::convert_library,
        send,
    )
    .map_err(|e| SourceError(format!("legacy library unavailable: {e:#}")))?;
    let config = read_old(&source.config, "config.json", legacy::convert_config, send)
        .map_err(|e| note(send, format!("skipping legacy config: {e:#}")))
        .ok();
    let assignments = read_old(
        &source.config,
        "assignments.json",
        legacy::convert_assignments,
        send,
    )
    .map_err(|e| note(send, format!("skipping legacy assignments: {e:#}")))
    .ok();
    let mut summary = Summary::default();
    for (index, item) in old.items.iter().enumerate() {
        if cancel.load(Ordering::Acquire) {
            break;
        }
        // Display the declared path only; never use the old dir field.
        let path = source.library.join(&item.id).join(&item.entry);
        let _ = send.send(Event::Started {
            index,
            total: old.items.len(),
            path: path.clone(),
        });
        let result = (|| -> std::result::Result<Option<Item>, Rejection> {
            let invalid = |e| Rejection::new(RejectCode::InvalidEntry, e);
            let path = contained_file(&source.library, &item.id, &item.entry).map_err(invalid)?;
            if let Some(existing) = library.find(&item.id) {
                let same_size = (|| -> Result<bool> {
                    let existing = contained_file(&paths.library, &existing.id, &existing.entry)?;
                    Ok(fs::metadata(existing)?.len() == fs::metadata(&path)?.len())
                })()
                .unwrap_or(false);
                if same_size {
                    note(
                        send,
                        format!(
                            "item {} already imported; preserving current title and fit",
                            item.id
                        ),
                    );
                    return Ok(None);
                }
                return Err(Rejection::new(
                    RejectCode::InvalidEntry,
                    format!(
                        "item {} exists but media is missing or size differs; delete this item from the new library before retrying",
                        item.id
                    ),
                ));
            }
            let storage = |e| Rejection::new(RejectCode::StorageFailed, e);
            real_directory(&paths.library, false).map_err(storage)?;
            remove_directory(&paths.library.join(&item.id)).map_err(storage)?;
            import::import_with_spec(
                &paths,
                &helper,
                &path,
                &mut library,
                cancel,
                send,
                index,
                &Spec {
                    id: Some(item.id.clone()),
                    title: Some(item.title.clone()),
                    fit: item.fit,
                    added_at: Some(item.added_at.clone()),
                },
                &Options::default(),
            )
        })();
        match result {
            Ok(Some(item)) => {
                summary.imported += 1;
                let _ = send.send(Event::Imported {
                    index,
                    id: item.id,
                    title: item.title,
                });
            }
            Ok(None) => {}
            Err(rejection) => {
                summary.rejected += 1;
                let stop = rejection.code == RejectCode::StorageFailed;
                let _ = send.send(Event::Rejected {
                    index,
                    path,
                    code: rejection.code,
                    detail: rejection.detail,
                });
                if stop {
                    return Ok(summary);
                }
            }
        }
    }
    summary.cancelled = cancel.load(Ordering::Acquire);
    if summary.cancelled {
        return Ok(summary);
    }
    if report.config.state == State::Missing {
        if let Some(config) = config {
            Store::save_config(&paths, &config)?;
        }
    } else {
        note(send, "preserving existing settings");
    }
    if let Some(old) = assignments {
        let mut assignments = report.assignments.value.context("missing assignments")?;
        let before = assignments.clone();
        let valid = |id: &str| {
            if library.find(id).is_some() {
                true
            } else {
                note(
                    send,
                    format!("discarding assignment referencing missing item {id}"),
                );
                false
            }
        };
        if let Some(id) = old.default
            && valid(&id)
            && assignments.default.is_none()
        {
            assignments.default = Some(id);
        }
        for (display, assignment) in old.displays {
            if assignment.item.as_deref().is_none_or(valid) {
                assignments.displays.entry(display).or_insert(assignment);
            }
        }
        for (display, alias) in old.aliases {
            assignments.aliases.entry(display).or_insert(alias);
        }
        if assignments != before {
            Store::save_assignments(&paths, &assignments)?;
        }
    }
    Ok(summary)
}

#[cfg(all(test, unix))]
mod tests {
    use super::*;
    use serde_json::{Value, json};
    use std::{
        collections::BTreeMap,
        os::unix::fs::{PermissionsExt, symlink},
        sync::atomic::AtomicU64,
        time::SystemTime,
    };
    use wallpaper_core::{
        displays::{Assignment, DisplayId},
        library::Fit,
    };

    struct Fixture {
        root: PathBuf,
        paths: Paths,
        source: LegacySource,
    }
    impl Fixture {
        fn new() -> Self {
            static SERIAL: AtomicU64 = AtomicU64::new(0);
            let root = std::env::temp_dir().join(format!(
                "wp-legacy-{}-{}",
                std::process::id(),
                SERIAL.fetch_add(1, Ordering::Relaxed)
            ));
            fs::create_dir(&root).unwrap();
            let source = LegacySource {
                config: root.join("old/config"),
                library: root.join("old/library"),
            };
            fs::create_dir_all(&source.config).unwrap();
            fs::create_dir_all(&source.library).unwrap();
            Self {
                paths: Paths {
                    config: root.join("new/config"),
                    library: root.join("new/library"),
                    cache: root.join("new/cache"),
                    logs: root.join("new/logs"),
                },
                root,
                source,
            }
        }
        fn item(&self, id: &str, entry: &str) -> Value {
            let path = self.source.library.join(id).join(entry);
            fs::create_dir_all(path.parent().unwrap()).unwrap();
            fs::write(path, b"media bytes").unwrap();
            json!({"id":id,"entry":entry,"type":"image","title":format!("title {id}"),"props":{"fit":"fit"},"addedAt":"2020-01-02T03:04:05Z","width":999,"dir":"/untrusted/do-not-read"})
        }
        fn write(&self, name: &str, value: Value) {
            fs::write(
                self.source.config.join(name),
                serde_json::to_vec(&value).unwrap(),
            )
            .unwrap();
        }
        fn library(&self, items: Vec<Value>) {
            self.write("library.json", json!({"schemaVersion":4,"items":items}));
        }
        fn helper(&self, script: &str) -> PathBuf {
            let path = self.root.join("helper.sh");
            fs::write(&path, format!("#!/bin/sh\n{script}\n")).unwrap();
            fs::set_permissions(&path, fs::Permissions::from_mode(0o700)).unwrap();
            path
        }
        fn run(&self, script: &str) -> (Result<Summary>, Vec<Event>) {
            let job = start(self.paths.clone(), self.helper(script), self.source.clone());
            let events = job.events.iter().collect();
            (job.join(), events)
        }
    }
    impl Drop for Fixture {
        fn drop(&mut self) {
            let _ = fs::remove_dir_all(&self.root);
        }
    }
    const SUCCESS: &str = "printf jpeg > \"$6\"\nprintf '%s\\n' '{\"ok\":{\"kind\":\"image\",\"width\":32,\"height\":16,\"durationSec\":null}}'";
    fn snapshot(root: &Path) -> BTreeMap<PathBuf, (Vec<u8>, SystemTime)> {
        fn walk(root: &Path, dir: &Path, result: &mut BTreeMap<PathBuf, (Vec<u8>, SystemTime)>) {
            for entry in fs::read_dir(dir).unwrap() {
                let entry = entry.unwrap();
                let metadata = entry.metadata().unwrap();
                if metadata.is_dir() {
                    walk(root, &entry.path(), result);
                } else {
                    result.insert(
                        entry.path().strip_prefix(root).unwrap().to_owned(),
                        (
                            fs::read(entry.path()).unwrap(),
                            metadata.modified().unwrap(),
                        ),
                    );
                }
            }
        }
        let mut result = BTreeMap::new();
        walk(root, root, &mut result);
        result
    }
    fn notes(events: &[Event]) -> String {
        events
            .iter()
            .filter_map(|e| match e {
                Event::Note { message } => Some(message.as_str()),
                _ => None,
            })
            .collect::<Vec<_>>()
            .join("\n")
    }
    fn codes(events: &[Event]) -> Vec<RejectCode> {
        events
            .iter()
            .filter_map(|e| match e {
                Event::Rejected { code, .. } => Some(*code),
                _ => None,
            })
            .collect()
    }

    #[test]
    fn default_paths_are_pure_for_override_mac_windows_and_missing_env() {
        for os in [Os::Mac, Os::Windows] {
            let source = default_source(os, |key| {
                (key == "WPAPP_HOME").then(|| "fake/portable".into())
            })
            .unwrap();
            assert_eq!(source.config, Path::new("fake/portable/config"));
            assert_eq!(source.library, Path::new("fake/portable/library"));
            assert!(default_source(os, |_| None).is_none());
        }
        let mac = default_source(Os::Mac, |key| match key {
            "HOME" => Some("fake/home".into()),
            "WPAPP_HOME" => Some("".into()),
            _ => None,
        })
        .unwrap();
        assert_eq!(
            mac.config,
            Path::new("fake/home/Library/Application Support/io.github.wpd.wallpaper")
        );
        assert_eq!(mac.library, mac.config.join("library"));
        let win = default_source(Os::Windows, |key| match key {
            "APPDATA" => Some("fake/roaming".into()),
            "LOCALAPPDATA" => Some("fake/local".into()),
            _ => None,
        })
        .unwrap();
        assert_eq!(win.config, Path::new("fake/roaming/WallpaperApp"));
        assert_eq!(win.library, Path::new("fake/local/WallpaperApp/library"));
    }

    #[test]
    fn import_preserves_source_bytes_mtimes_metadata_and_retry_user_edits() {
        let f = Fixture::new();
        let mut one = f.item("one", "nested/雪.PNG");
        one["entry"] = json!("nested\\雪.PNG");
        let mut two = f.item("two", "two.jpg");
        two["title"] = json!("");
        two["addedAt"] = json!("");
        f.library(vec![one, two]);
        f.write(
            "config.json",
            json!({"schemaVersion":2,"ui":{"lang":"zh-CN"},"pause":{"batteryPolicy":"fps15"}}),
        );
        f.write("assignments.json", json!({"schemaVersion":3,"default":"one","displays":{"old-id":"two"},"options":{"old-id":{"paused":true}},"aliases":{"old-id":"Desk"}}));
        fs::write(f.source.config.join("state.json"), b"not imported").unwrap();
        fs::write(f.source.library.join("old-thumb.jpg"), b"not imported").unwrap();
        let old = snapshot(&f.root.join("old"));
        let (result, events) = f.run(SUCCESS);
        assert_eq!(
            result.unwrap(),
            Summary {
                imported: 2,
                rejected: 0,
                cancelled: false
            }
        );
        assert!(notes(&events).contains("fps15 converted"));
        assert_eq!(snapshot(&f.root.join("old")), old);
        let report = Store::inspect(&f.paths);
        let mut library = report.library.value.unwrap();
        assert_eq!(library.items[0].title, "title one");
        assert_eq!(library.items[0].fit, Fit::Fit);
        assert_eq!(library.items[0].added_at, "2020-01-02T03:04:05Z");
        assert_eq!(library.items[0].entry, "media.png");
        assert_eq!(library.items[0].width, Some(32));
        assert_eq!(library.items[1].title, "two");
        assert!(library.items[1].added_at.ends_with('Z'));
        assert!(f.paths.cache.join("one/thumb.jpg").is_file());
        let mut config = report.config.value.unwrap();
        assert_eq!(config.ui.lang, wallpaper_core::config::Lang::ZhCn);
        let mut assignments = report.assignments.value.unwrap();
        assert_eq!(assignments.default.as_deref(), Some("one"));
        assert!(assignments.displays[&DisplayId("old-id".into())].paused);
        library.items[0].title = "user title".into();
        library.items[0].fit = Fit::Stretch;
        config.ui.lang = wallpaper_core::config::Lang::En;
        assignments.default = Some("two".into());
        assignments
            .displays
            .get_mut(&DisplayId("old-id".into()))
            .unwrap()
            .item = Some("one".into());
        assignments
            .aliases
            .insert(DisplayId("old-id".into()), "User desk".into());
        Store::save_library(&f.paths, &library).unwrap();
        Store::save_config(&f.paths, &config).unwrap();
        Store::save_assignments(&f.paths, &assignments).unwrap();
        let new = snapshot(&f.root.join("new"));
        let (result, events) = f.run("exit 99");
        assert_eq!(result.unwrap(), Summary::default());
        assert_eq!(notes(&events).matches("already imported").count(), 2);
        assert!(notes(&events).contains("preserving existing settings"));
        assert_eq!(snapshot(&f.root.join("new")), new);
        assert_eq!(snapshot(&f.root.join("old")), old);
        // Copy isolation: modifying the new media cannot change the old inode.
        fs::write(f.paths.library.join("one/media.png"), b"different").unwrap();
        assert_eq!(snapshot(&f.root.join("old")), old);
    }

    #[test]
    fn failure_retry_replaces_orphan_and_rejects_existing_size_mismatch() {
        let f = Fixture::new();
        f.library(vec![f.item("one", "one.png"), f.item("two", "two.png")]);
        fs::create_dir_all(f.paths.library.join(".staging-abandoned")).unwrap();
        fs::create_dir_all(f.paths.library.join("two")).unwrap();
        fs::write(f.paths.library.join("two/orphan"), b"partial").unwrap();
        let (result, _) = f.run(&format!(
            "case \"$2\" in *staging-two*) exit 99;; esac\n{SUCCESS}"
        ));
        assert_eq!(result.unwrap().imported, 1);
        assert!(!f.paths.library.join(".staging-abandoned").exists());
        assert!(!f.paths.library.join("two").exists());
        fs::create_dir_all(f.paths.library.join("two")).unwrap();
        fs::write(f.paths.library.join("two/orphan"), b"partial").unwrap();
        let (result, events) = f.run(SUCCESS);
        assert!(!f.paths.library.join("two/orphan").exists());
        assert_eq!(result.unwrap().imported, 1);
        assert!(notes(&events).contains("one already imported"));
        fs::write(f.paths.library.join("one/media.png"), b"short").unwrap();
        let (result, events) = f.run("exit 99");
        assert_eq!(result.unwrap().rejected, 1);
        assert_eq!(codes(&events), [RejectCode::InvalidEntry]);
        assert!(events.iter().any(
            |e| matches!(e, Event::Rejected { detail, .. } if detail.contains("delete this item"))
        ));
    }

    #[test]
    fn cancel_in_probe_then_retry_only_missing_and_defer_settings() {
        let f = Fixture::new();
        f.library(vec![f.item("one", "one.png"), f.item("two", "two.png")]);
        f.write("config.json", json!({}));
        f.write("assignments.json", json!({"default":"one"}));
        let helper = f.helper(&format!(
            "case \"$2\" in *staging-two*) exec /bin/sleep 20;; esac\n{SUCCESS}"
        ));
        let job = start(f.paths.clone(), helper, f.source.clone());
        let mut finished = false;
        loop {
            match job
                .events
                .recv_timeout(std::time::Duration::from_secs(5))
                .unwrap()
            {
                Event::Probing { index: 1 } => job.cancel.store(true, Ordering::Release),
                Event::Finished { cancelled, .. } => {
                    assert!(cancelled);
                    finished = true;
                }
                _ => {}
            }
            if finished {
                break;
            }
        }
        assert_eq!(
            job.join().unwrap(),
            Summary {
                imported: 1,
                rejected: 0,
                cancelled: true
            }
        );
        assert!(!f.paths.config.join("config.json").exists());
        assert!(!f.paths.config.join("assignments.json").exists());
        assert!(!f.paths.library.join(".staging-two").exists());
        let (result, _) = f.run(SUCCESS);
        assert_eq!(result.unwrap().imported, 1);
        assert!(f.paths.config.join("config.json").is_file());
    }

    #[test]
    fn unsupported_types_note_and_video_gif_uses_image_probe() {
        let f = Fixture::new();
        let mut gif = f.item("gif", "animation.gif");
        gif["type"] = json!("video");
        f.library(vec![
            json!({"id":"web","type":"web","entry":"index.html"}),
            gif,
        ]);
        let (result, events) = f.run("test \"$4\" = image || exit 99\nprintf '%s\\n' '{\"rejected\":{\"code\":\"animated_image\",\"detail\":\"animated\"}}'\nexit 3");
        assert_eq!(result.unwrap().rejected, 1);
        assert_eq!(codes(&events), [RejectCode::AnimatedImage]);
        assert!(notes(&events).contains("skipped unsupported type"));
    }

    #[test]
    fn unsafe_ids_entries_and_symlinks_never_copy_external_files() {
        let f = Fixture::new();
        let safe = f.item("one", "safe.png");
        let external = f.root.join("outside.png");
        fs::write(&external, b"external").unwrap();
        symlink(&external, f.source.library.join("one/link.png")).unwrap();
        let outside = f.root.join("outside-dir");
        fs::create_dir(&outside).unwrap();
        fs::write(outside.join("x.png"), b"external").unwrap();
        symlink(&outside, f.source.library.join("linked-id")).unwrap();
        let mut items = Vec::new();
        for (id, entry) in [
            ("../outside", "x.png"),
            ("one", "../outside.png"),
            ("one", external.to_str().unwrap()),
            ("one", "link.png"),
            ("linked-id", "x.png"),
            ("", "x.png"),
            ("one", ""),
            ("one", "C:\\outside.png"),
            ("one", "..\\outside.png"),
        ] {
            let mut item = safe.clone();
            item["id"] = json!(id);
            item["entry"] = json!(entry);
            items.push(item);
        }
        f.library(items);
        let (result, events) = f.run("exit 99");
        assert_eq!(result.unwrap().rejected, 9);
        assert!(
            codes(&events)
                .iter()
                .all(|c| *c == RejectCode::InvalidEntry)
        );
        assert_eq!(fs::read_dir(&f.paths.library).unwrap().count(), 0);
        assert_eq!(fs::read(external).unwrap(), b"external");
    }

    #[test]
    fn backups_read_only_and_both_invalid_or_future_schema_fail() {
        let f = Fixture::new();
        f.library(vec![f.item("one", "one.png")]);
        f.write("config.json", json!({"ui":{"lang":"zh-CN"}}));
        f.write("assignments.json", json!({"default":"one"}));
        for name in ["library.json", "config.json", "assignments.json"] {
            fs::rename(
                f.source.config.join(name),
                f.source.config.join(format!("{name}.bak")),
            )
            .unwrap();
            fs::write(f.source.config.join(name), b"broken").unwrap();
        }
        let before = snapshot(&f.root.join("old"));
        let (result, events) = f.run(SUCCESS);
        assert_eq!(result.unwrap().imported, 1);
        assert_eq!(notes(&events).matches("using read-only backup").count(), 3);
        assert_eq!(snapshot(&f.root.join("old")), before);
        f.write("library.json", json!({"schemaVersion":99}));
        let (result, _) = f.run(SUCCESS);
        let error = result.unwrap_err();
        assert!(error.downcast_ref::<SourceError>().is_some());
        assert!(error.to_string().contains("exceeds"));
        fs::write(f.source.config.join("library.json"), b"broken").unwrap();
        fs::write(f.source.config.join("library.json.bak"), b"broken").unwrap();
        assert!(
            f.run(SUCCESS)
                .0
                .unwrap_err()
                .downcast_ref::<SourceError>()
                .is_some()
        );
        fs::remove_file(f.source.config.join("library.json")).unwrap();
        fs::remove_file(f.source.config.join("library.json.bak")).unwrap();
        assert!(
            f.run(SUCCESS)
                .0
                .unwrap_err()
                .downcast_ref::<SourceError>()
                .is_some()
        );
    }

    #[test]
    fn unreadable_primary_uses_backup_optional_invalid_files_only_note() {
        let f = Fixture::new();
        f.library(vec![]);
        fs::rename(
            f.source.config.join("library.json"),
            f.source.config.join("library.json.bak"),
        )
        .unwrap();
        fs::create_dir(f.source.config.join("library.json")).unwrap();
        f.write("config.json", json!({"schemaVersion":99}));
        fs::write(f.source.config.join("assignments.json"), b"broken").unwrap();
        let (result, events) = f.run(SUCCESS);
        assert_eq!(result.unwrap(), Summary::default());
        let notes = notes(&events);
        assert!(notes.contains("using read-only backup"));
        assert!(notes.contains("skipping legacy config"));
        assert!(notes.contains("skipping legacy assignments"));
        assert!(!f.paths.config.exists());
    }

    #[test]
    fn corrupt_new_store_has_no_writes_or_cleanup() {
        let f = Fixture::new();
        f.library(vec![f.item("one", "one.png")]);
        fs::create_dir_all(&f.paths.config).unwrap();
        fs::write(f.paths.config.join("library.json"), b"broken").unwrap();
        fs::create_dir_all(f.paths.library.join(".staging-abandoned")).unwrap();
        let before = snapshot(&f.root);
        // Helper must exist before snapshot to make the whole-tree comparison exact.
        let helper = f.helper(SUCCESS);
        let before_new = snapshot(&f.root.join("new"));
        assert!(
            start(f.paths.clone(), helper, f.source.clone())
                .join()
                .is_err()
        );
        assert_eq!(snapshot(&f.root.join("new")), before_new);
        assert_eq!(
            fs::read(f.paths.config.join("library.json")).unwrap(),
            before[&PathBuf::from("new/config/library.json")].0
        );
        assert!(f.paths.library.join(".staging-abandoned").is_dir());
    }

    #[test]
    fn assignments_add_only_discard_missing_and_do_not_rewrite_when_unchanged() {
        let f = Fixture::new();
        f.library(vec![f.item("one", "one.png"), f.item("two", "two.png")]);
        f.write(
            "assignments.json",
            json!({"default":"two","displays":{"existing":"two","new":"two","missing":"absent"}}),
        );
        let mut a = wallpaper_core::displays::Assignments {
            default: Some("one".into()),
            ..Default::default()
        };
        a.displays.insert(
            DisplayId("existing".into()),
            Assignment {
                item: Some("one".into()),
                paused: true,
            },
        );
        Store::save_assignments(&f.paths, &a).unwrap();
        let (result, events) = f.run(SUCCESS);
        assert_eq!(result.unwrap().imported, 2);
        assert!(notes(&events).contains("missing item absent"));
        let merged = Store::inspect(&f.paths).assignments.value.unwrap();
        assert_eq!(merged.default, a.default);
        assert_eq!(
            merged.displays[&DisplayId("existing".into())],
            a.displays[&DisplayId("existing".into())]
        );
        assert_eq!(
            merged.displays[&DisplayId("new".into())].item.as_deref(),
            Some("two")
        );
        assert!(!merged.displays.contains_key(&DisplayId("missing".into())));
        let before = snapshot(&f.paths.config);
        f.run("exit 99").0.unwrap();
        assert_eq!(snapshot(&f.paths.config), before);
        f.write("assignments.json", json!({"default":"absent"}));
        Store::save_assignments(&f.paths, &Default::default()).unwrap();
        let (_, events) = f.run("exit 99");
        assert!(notes(&events).contains("missing item absent"));
        assert_eq!(
            Store::inspect(&f.paths).assignments.value.unwrap().default,
            None
        );
    }

    #[test]
    fn destination_overlapping_legacy_library_is_rejected_before_cleanup() {
        let f = Fixture::new();
        f.library(vec![f.item("one", "one.png")]);
        fs::create_dir(f.source.library.join(".staging-preserve")).unwrap();
        let mut paths = f.paths.clone();
        paths.library = f.source.library.clone();
        let before = snapshot(&f.root.join("old"));
        assert!(
            start(paths, f.helper(SUCCESS), f.source.clone())
                .join()
                .is_err()
        );
        assert_eq!(snapshot(&f.root.join("old")), before);
        assert!(f.source.library.join(".staging-preserve").exists());
    }
}
