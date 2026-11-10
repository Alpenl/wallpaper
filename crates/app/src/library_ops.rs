use crate::storage::{Paths, Report, Store};
use anyhow::{Context, Result, bail, ensure};
use serde::Serialize;
use std::{
    fs,
    io::ErrorKind,
    path::{Path, PathBuf},
};
use wallpaper_core::{
    displays::{DisplayId, DisplayInfo},
    library::{self, Fit, Item},
};

pub(crate) fn open(paths: &Paths) -> Result<Report> {
    // Preflight prevents recovery of one file from modifying storage when another
    // file is corrupt. Store::open still owns normal backup recovery.
    ensure!(
        !Store::inspect(paths).has_errors(),
        "storage is corrupt; use --store-check"
    );
    let report = Store::open(paths);
    ensure!(
        !report.has_errors(),
        "storage is corrupt; use --store-check"
    );
    Ok(report)
}

pub(crate) fn safe_id(id: &str) -> Result<()> {
    ensure!(library::is_safe_component(id), "unsafe library id: {id:?}");
    Ok(())
}

// Refuse symlink/reparse directory components. Never follow a cache or library
// directory supplied by an old or manually edited store.
pub(crate) fn real_directory(path: &Path, create: bool) -> Result<()> {
    match fs::symlink_metadata(path) {
        Ok(meta) => ensure!(
            meta.is_dir() && !is_link(&meta),
            "not a real directory: {}",
            path.display()
        ),
        Err(e) if e.kind() == ErrorKind::NotFound => {
            if create {
                fs::create_dir_all(path)?;
            }
        }
        Err(e) => return Err(e.into()),
    }
    Ok(())
}
fn is_link(meta: &fs::Metadata) -> bool {
    #[cfg(windows)]
    {
        use std::os::windows::fs::MetadataExt;
        meta.file_attributes() & 0x400 != 0
    }
    #[cfg(not(windows))]
    {
        meta.file_type().is_symlink()
    }
}
pub(crate) fn remove_directory(path: &Path) -> Result<()> {
    if let Some(parent) = path.parent() {
        real_directory(parent, false)?;
    }
    match fs::symlink_metadata(path) {
        Ok(meta) if is_link(&meta) => {
            #[cfg(windows)]
            if meta.is_dir() {
                fs::remove_dir(path)?;
                return Ok(());
            }
            fs::remove_file(path)?;
        }
        Ok(meta) if meta.is_dir() => fs::remove_dir_all(path)?,
        Ok(_) => bail!("not a directory: {}", path.display()),
        Err(e) if e.kind() == ErrorKind::NotFound => {}
        Err(e) => return Err(e.into()),
    }
    Ok(())
}

#[derive(Debug, Serialize)]
pub struct ListedItem {
    #[serde(flatten)]
    pub item: Item,
    pub thumbnail: Option<PathBuf>,
}
pub fn list(paths: &Paths, query: &str) -> Result<Vec<ListedItem>> {
    let report = open(paths)?;
    let library = report.library.value.context("missing library")?;
    library::search(&library.items, query)
        .into_iter()
        .map(|item| {
            safe_id(&item.id)?;
            let cache = paths.cache.join(&item.id);
            let thumbnail = if real_directory(&paths.cache, false).is_ok()
                && real_directory(&cache, false).is_ok()
            {
                let path = cache.join("thumb.jpg");
                fs::symlink_metadata(&path)
                    .ok()
                    .filter(|m| m.is_file() && !is_link(m))
                    .map(|_| path)
            } else {
                None
            };
            Ok(ListedItem {
                item: item.clone(),
                thumbnail,
            })
        })
        .collect()
}

#[derive(Debug, Serialize)]
pub struct DeletePreview {
    pub item: Item,
    pub directory: PathBuf,
    pub cache: PathBuf,
    pub affects_default: bool,
    pub displays: Vec<DisplayId>,
}
pub fn delete_preview(paths: &Paths, id: &str) -> Result<DeletePreview> {
    safe_id(id)?;
    // Confirmation preview is strictly read-only, including backup recovery.
    let report = Store::inspect(paths);
    ensure!(
        !report.has_errors(),
        "storage is corrupt; use --store-check"
    );
    let library = report.library.value.context("missing library")?;
    let assignments = report.assignments.value.context("missing assignments")?;
    Ok(DeletePreview {
        item: library.find(id).context("library item not found")?.clone(),
        directory: paths.library.join(id),
        cache: paths.cache.join(id),
        affects_default: assignments.default.as_deref() == Some(id),
        displays: assignments
            .displays
            .into_iter()
            .filter(|(_, a)| a.item.as_deref() == Some(id))
            .map(|(id, _)| id)
            .collect(),
    })
}
pub fn delete(paths: &Paths, id: &str) -> Result<()> {
    safe_id(id)?;
    let report = open(paths)?;
    let mut library = report.library.value.context("missing library")?;
    ensure!(library.find(id).is_some(), "library item not found: {id}");
    real_directory(&paths.library, false)?;
    real_directory(&paths.cache, false)?;
    let mut assignments = report.assignments.value.context("missing assignments")?;
    assignments.remove_item(id);
    Store::save_assignments(paths, &assignments)?;
    library.items.retain(|item| item.id != id);
    Store::save_library(paths, &library)?;
    remove_directory(&paths.library.join(id))?;
    remove_directory(&paths.cache.join(id))
}
pub fn assign_display(
    paths: &Paths,
    id: &str,
    display: &DisplayId,
    connected: &[DisplayInfo],
) -> Result<()> {
    safe_id(id)?;
    let report = open(paths)?;
    ensure!(
        report
            .library
            .value
            .context("missing library")?
            .find(id)
            .is_some(),
        "library item not found: {id}"
    );
    let mut assignments = report.assignments.value.context("missing assignments")?;
    ensure!(
        assignments.displays.contains_key(display)
            || connected
                .iter()
                .any(|d| &d.id == display || d.legacy_ids.contains(display)),
        "unknown display; use --displays"
    );
    assignments.assign_display(display.clone(), Some(id.into()));
    Store::save_assignments(paths, &assignments)
}
pub fn assign_all(paths: &Paths, id: &str) -> Result<()> {
    safe_id(id)?;
    let report = open(paths)?;
    ensure!(
        report
            .library
            .value
            .context("missing library")?
            .find(id)
            .is_some(),
        "library item not found: {id}"
    );
    let mut assignments = report.assignments.value.context("missing assignments")?;
    assignments.assign_all(Some(id.into()));
    Store::save_assignments(paths, &assignments)
}
pub fn set_fit(paths: &Paths, id: &str, fit: Fit) -> Result<()> {
    safe_id(id)?;
    let report = open(paths)?;
    let mut library = report.library.value.context("missing library")?;
    library
        .items
        .iter_mut()
        .find(|item| item.id == id)
        .context("library item not found")?
        .fit = fit;
    Store::save_library(paths, &library)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::atomic::{AtomicU64, Ordering};
    use wallpaper_core::{
        displays::{Assignment, Assignments, Frame},
        library::{Kind, Library},
    };
    struct Fixture {
        root: PathBuf,
        paths: Paths,
    }
    impl Fixture {
        fn new() -> Self {
            static SERIAL: AtomicU64 = AtomicU64::new(0);
            let root = std::env::temp_dir().join(format!(
                "wp-library-{}-{}",
                std::process::id(),
                SERIAL.fetch_add(1, Ordering::Relaxed)
            ));
            fs::create_dir(&root).unwrap();
            let paths = Paths {
                config: root.join("config"),
                library: root.join("library"),
                cache: root.join("cache"),
                logs: root.join("logs"),
            };
            let item = Item {
                id: "one".into(),
                title: "雪 ÄBC".into(),
                kind: Kind::Image,
                entry: root.join("external.png").to_string_lossy().into_owned(),
                width: Some(20),
                height: Some(10),
                duration_sec: None,
                size_bytes: Some(5),
                added_at: wallpaper_core::timestamp(),
                fit: Fit::Fill,
            };
            fs::write(&item.entry, b"keep").unwrap();
            Store::save_library(
                &paths,
                &Library {
                    items: vec![item],
                    ..Library::default()
                },
            )
            .unwrap();
            let mut assignments = Assignments {
                default: Some("one".into()),
                ..Assignments::default()
            };
            assignments.displays.insert(
                DisplayId("screen".into()),
                Assignment {
                    item: Some("one".into()),
                    paused: true,
                },
            );
            Store::save_assignments(&paths, &assignments).unwrap();
            fs::create_dir_all(paths.library.join("one")).unwrap();
            fs::write(paths.library.join("one/media.png"), b"media").unwrap();
            fs::create_dir_all(paths.cache.join("one")).unwrap();
            fs::write(paths.cache.join("one/thumb.jpg"), b"jpeg").unwrap();
            Self { root, paths }
        }
    }
    impl Drop for Fixture {
        fn drop(&mut self) {
            let _ = fs::remove_dir_all(&self.root);
        }
    }
    #[test]
    fn delete_clears_default_preserves_pause_and_never_deletes_absolute_entry() {
        let f = Fixture::new();
        let before = fs::read(f.paths.config.join("library.json")).unwrap();
        let preview = delete_preview(&f.paths, "one").unwrap();
        assert!(preview.affects_default);
        assert_eq!(preview.displays, [DisplayId("screen".into())]);
        assert_eq!(
            fs::read(f.paths.config.join("library.json")).unwrap(),
            before
        );
        assert_eq!(fs::read_dir(&f.paths.config).unwrap().count(), 2);
        delete(&f.paths, "one").unwrap();
        assert_eq!(fs::read(f.root.join("external.png")).unwrap(), b"keep");
        assert!(!f.paths.library.join("one").exists());
        assert!(!f.paths.cache.join("one").exists());
        let report = Store::inspect(&f.paths);
        assert!(report.library.value.unwrap().items.is_empty());
        let a = report.assignments.value.unwrap();
        assert_eq!(a.default, None);
        assert_eq!(
            a.displays[&DisplayId("screen".into())],
            Assignment {
                item: None,
                paused: true
            }
        );
    }
    #[test]
    fn assignments_save_failure_does_not_change_library_or_media() {
        let f = Fixture::new();
        let before = fs::read(f.paths.config.join("library.json")).unwrap();
        let assignments = fs::read(f.paths.config.join("assignments.json")).unwrap();
        // Main file remains valid for Store::open. Backup directory makes save fail.
        fs::create_dir(f.paths.config.join("assignments.json.bak")).unwrap();
        assert!(delete(&f.paths, "one").is_err());
        assert_eq!(
            fs::read(f.paths.config.join("library.json")).unwrap(),
            before
        );
        assert_eq!(
            fs::read(f.paths.config.join("assignments.json")).unwrap(),
            assignments
        );
        assert_eq!(
            fs::read(f.paths.library.join("one/media.png")).unwrap(),
            b"media"
        );
        assert!(f.paths.cache.join("one/thumb.jpg").is_file());
    }
    #[test]
    fn library_save_failure_leaves_media_and_assignments_already_cleared() {
        let f = Fixture::new();
        let before = fs::read(f.paths.config.join("library.json")).unwrap();
        fs::create_dir(f.paths.config.join("library.json.bak")).unwrap();
        assert!(delete(&f.paths, "one").is_err());
        assert_eq!(
            fs::read(f.paths.config.join("library.json")).unwrap(),
            before
        );
        assert!(f.paths.library.join("one/media.png").is_file());
        assert_eq!(
            Store::inspect(&f.paths).assignments.value.unwrap().default,
            None
        );
    }
    #[test]
    fn missing_directory_delete_succeeds_and_unsafe_ids_fail() {
        let f = Fixture::new();
        for id in [
            "",
            ".",
            "..",
            "../external",
            "a/b",
            "a\\b",
            "C:foo",
            ".hidden",
            "a\0b",
            "missing",
        ] {
            assert!(delete(&f.paths, id).is_err());
            assert!(delete_preview(&f.paths, id).is_err());
            assert!(assign_all(&f.paths, id).is_err());
            assert!(set_fit(&f.paths, id, Fit::Fit).is_err());
        }
        fs::remove_dir_all(f.paths.library.join("one")).unwrap();
        fs::remove_dir_all(f.paths.cache.join("one")).unwrap();
        delete(&f.paths, "one").unwrap();
    }
    #[test]
    fn list_search_thumbnail_and_fit() {
        let f = Fixture::new();
        let items = list(&f.paths, " äbc ").unwrap();
        assert_eq!(items.len(), 1);
        assert_eq!(
            items[0].thumbnail,
            Some(f.paths.cache.join("one/thumb.jpg"))
        );
        assert!(list(&f.paths, "absent").unwrap().is_empty());
        fs::remove_file(f.paths.cache.join("one/thumb.jpg")).unwrap();
        assert!(list(&f.paths, "").unwrap()[0].thumbnail.is_none());
        for fit in [Fit::Fit, Fit::Stretch, Fit::Fill] {
            set_fit(&f.paths, "one", fit).unwrap();
            assert_eq!(list(&f.paths, "").unwrap()[0].item.fit, fit);
        }
    }
    #[test]
    fn assignments_accept_connected_legacy_and_saved_ids_only() {
        let f = Fixture::new();
        let displays = [DisplayInfo {
            id: DisplayId("connected".into()),
            legacy_ids: vec![DisplayId("legacy".into())],
            name: "Screen".into(),
            primary: true,
            frame: Frame {
                x: 0,
                y: 0,
                width: 1920,
                height: 1080,
            },
            scale: 1.,
        }];
        for id in ["connected", "legacy", "screen"] {
            assign_display(&f.paths, "one", &DisplayId(id.into()), &displays).unwrap();
        }
        assert!(
            assign_display(&f.paths, "one", &DisplayId("typo".into()), &displays)
                .unwrap_err()
                .to_string()
                .contains("--displays")
        );
        assert!(
            assign_display(&f.paths, "missing", &DisplayId("screen".into()), &displays).is_err()
        );
        assign_all(&f.paths, "one").unwrap();
        let a = Store::inspect(&f.paths).assignments.value.unwrap();
        assert_eq!(a.default.as_deref(), Some("one"));
        assert!(
            a.displays
                .values()
                .all(|a| a.item.as_deref() == Some("one"))
        );
        assert!(a.displays[&DisplayId("screen".into())].paused);
    }
    #[test]
    fn corrupt_store_blocks_all_operations_without_recovery_writes() {
        let f = Fixture::new();
        // A valid library backup must not be restored if config is corrupt.
        fs::copy(
            f.paths.config.join("library.json"),
            f.paths.config.join("library.json.bak"),
        )
        .unwrap();
        fs::write(f.paths.config.join("library.json"), b"broken library").unwrap();
        fs::write(f.paths.config.join("config.json"), b"broken config").unwrap();
        assert!(list(&f.paths, "").is_err());
        assert!(delete(&f.paths, "one").is_err());
        assert!(delete_preview(&f.paths, "one").is_err());
        assert!(assign_all(&f.paths, "one").is_err());
        assert!(assign_display(&f.paths, "one", &DisplayId("screen".into()), &[]).is_err());
        assert!(set_fit(&f.paths, "one", Fit::Fit).is_err());
        assert_eq!(
            fs::read(f.paths.config.join("library.json")).unwrap(),
            b"broken library"
        );
        assert_eq!(fs::read_dir(&f.paths.config).unwrap().count(), 4);
    }
    #[cfg(unix)]
    #[test]
    fn symlink_item_directory_and_thumbnail_do_not_escape() {
        use std::os::unix::fs::symlink;
        let f = Fixture::new();
        fs::remove_dir_all(f.paths.library.join("one")).unwrap();
        fs::remove_dir_all(f.paths.cache.join("one")).unwrap();
        let external = f.root.join("external");
        fs::create_dir(&external).unwrap();
        fs::write(external.join("keep"), b"keep").unwrap();
        symlink(&external, f.paths.library.join("one")).unwrap();
        symlink(&external, f.paths.cache.join("one")).unwrap();
        assert!(list(&f.paths, "").unwrap()[0].thumbnail.is_none());
        delete(&f.paths, "one").unwrap();
        assert_eq!(fs::read(external.join("keep")).unwrap(), b"keep");
        assert!(!f.paths.library.join("one").is_symlink());
        assert!(!f.paths.cache.join("one").is_symlink());
    }
}
