//! Read-only collection and explicit, privacy-preserving diagnostic projection.
use crate::storage::{FileReport, Paths, State, Store};
use anyhow::{Context, Result, bail, ensure};
use serde_json::{Value, json};
use std::{
    collections::BTreeMap,
    fs,
    io::{Read, Seek, SeekFrom, Write},
    path::{Path, PathBuf},
};
use wallpaper_core::{
    config::Settings,
    displays::DisplayInfo,
    redact::{Omitted, Redactor},
};
const TAIL_LIMIT: u64 = 1024 * 1024;
const LINE_LIMIT: usize = 16 * 1024;
const EXPORT_LIMIT: usize = 5 * 1024 * 1024;
#[derive(Default)]
struct Omissions(BTreeMap<&'static str, u64>);
impl Omissions {
    fn add(&mut self, reason: &'static str, count: u64) {
        if count > 0 {
            *self.0.entry(reason).or_default() += count;
        }
    }
}
fn state<T>(report: &FileReport<T>) -> &'static str {
    match report.state {
        State::Loaded => "ok",
        State::Missing => "missing",
        State::Recovered => "recovered",
        State::Newer => "newer",
        State::Corrupt if report.detail.contains("read failed") => "read-failed",
        State::Corrupt => "corrupt",
    }
}
fn settings(s: &Settings) -> Value {
    json!({"description":"Parsed effective configuration; defaults are used when missing. Unreadable configuration is null.",
        "schemaVersion":s.schema_version,
        "pause":{"batteryPolicy":s.pause.battery_policy,"lowBatteryPct":s.pause.low_battery_pct,"resumeDebounceMs":s.pause.resume_debounce_ms,"onFullscreen":s.pause.on_fullscreen,"onCovered":s.pause.on_covered},
        "startup":{"launchAtLogin":s.startup.launch_at_login,"openSettings":s.startup.open_settings},
        "ui":{"lang":s.ui.lang,"theme":s.ui.theme}})
}
fn tail(path: &Path, redactor: &Redactor, omissions: &mut Omissions) -> Vec<String> {
    let metadata = match fs::symlink_metadata(path) {
        Ok(m) if m.is_file() => m,
        Ok(_) => {
            omissions.add("log-not-regular", 1);
            return vec![];
        }
        Err(e) => {
            omissions.add(
                if e.kind() == std::io::ErrorKind::NotFound {
                    "log-missing"
                } else {
                    "log-read-failed"
                },
                1,
            );
            return vec![];
        }
    };
    let result = (|| -> std::io::Result<Vec<u8>> {
        let mut file = fs::File::open(path)?;
        let start = metadata.len().saturating_sub(TAIL_LIMIT);
        file.seek(SeekFrom::Start(start))?;
        let mut bytes = Vec::new();
        file.take(TAIL_LIMIT).read_to_end(&mut bytes)?;
        omissions.add("log-prefix-bytes", start);
        if start > 0 {
            if let Some(n) = bytes.iter().position(|b| *b == b'\n') {
                bytes.drain(..=n);
            } else {
                bytes.clear();
            }
            omissions.add("partial-first-line", 1);
        }
        if !bytes.is_empty() && bytes.last() != Some(&b'\n') {
            let end = bytes.iter().rposition(|b| *b == b'\n').map_or(0, |n| n + 1);
            bytes.truncate(end);
            omissions.add("partial-last-line", 1);
        }
        Ok(bytes)
    })();
    let bytes = match result {
        Ok(b) => b,
        Err(_) => {
            omissions.add("log-read-failed", 1);
            return vec![];
        }
    };
    let mut lines = Vec::new();
    for raw in bytes.split_inclusive(|b| *b == b'\n') {
        let raw = &raw[..raw.len() - 1];
        if raw.len() > LINE_LIMIT {
            omissions.add("oversized-line", 1);
            continue;
        }
        let text = String::from_utf8_lossy(raw);
        match redactor.redact(text.trim_end_matches('\r')) {
            Ok(line) => lines.push(line),
            Err(Omitted::InvalidUnicode) => omissions.add("invalid-unicode-line", 1),
            Err(Omitted::Unsafe) => omissions.add("unsafe-line", 1),
        }
    }
    lines
}
fn extension(entry: &str) -> String {
    let ext = Path::new(entry)
        .extension()
        .and_then(|s| s.to_str())
        .unwrap_or("")
        .to_ascii_lowercase();
    if [
        "mp4", "m4v", "mov", "webm", "gif", "png", "jpg", "jpeg", "webp", "bmp",
    ]
    .contains(&ext.as_str())
    {
        ext
    } else {
        "other".into()
    }
}
pub fn collect(
    paths: &Paths,
    displays: Option<&[DisplayInfo]>,
    extra_known: &[String],
) -> Result<Vec<u8>> {
    let mut omissions = Omissions::default();
    if displays.is_none() {
        omissions.add("displays-unavailable", 1);
    }
    let displays = displays.unwrap_or_default();
    let report = Store::inspect(paths);
    let mut known = extra_known.to_vec();
    for path in [&paths.config, &paths.library, &paths.cache, &paths.logs] {
        known.push(path.to_string_lossy().into_owned());
    }
    let mut kinds: BTreeMap<&str, usize> = BTreeMap::new();
    let mut extensions: BTreeMap<String, usize> = BTreeMap::new();
    let mut fits: BTreeMap<&str, usize> = BTreeMap::new();
    let library = report.library.value.as_ref();
    if let Some(library) = library {
        for item in &library.items {
            use wallpaper_core::library::{Fit, Kind};
            *kinds
                .entry(match item.kind {
                    Kind::Image => "image",
                    Kind::Video => "video",
                })
                .or_default() += 1;
            *extensions.entry(extension(&item.entry)).or_default() += 1;
            *fits
                .entry(match item.fit {
                    Fit::Fill => "fill",
                    Fit::Fit => "fit",
                    Fit::Stretch => "stretch",
                })
                .or_default() += 1;
            known.extend([item.title.clone(), item.id.clone(), item.entry.clone()]);
            let dir = paths.library.join(&item.id);
            known.push(dir.to_string_lossy().into_owned());
            known.push(dir.join(&item.entry).to_string_lossy().into_owned());
            if let Some(name) = item.entry.rsplit(['/', '\\']).next() {
                known.push(name.into());
            }
        }
    }
    let assignments = report.assignments.value.as_ref();
    if let Some(a) = assignments {
        known.extend(a.default.iter().cloned());
        known.extend(a.displays.values().filter_map(|a| a.item.clone()));
        known.extend(a.aliases.values().cloned());
        known.extend(
            a.displays
                .keys()
                .chain(a.aliases.keys())
                .map(|id| id.0.clone()),
        );
    }
    for display in displays {
        known.extend([display.name.clone(), display.id.0.clone()]);
        known.extend(display.legacy_ids.iter().map(|id| id.0.clone()));
    }
    let redactor = Redactor::new(&known);
    let old = tail(&paths.logs.join("app.log.1"), &redactor, &mut omissions);
    let current = tail(&paths.logs.join("app.log"), &redactor, &mut omissions);
    let display_rows: Vec<_> = displays.iter().enumerate().map(|(n,d)| json!({"display":format!("display-{}", n+1),"size":{"width":d.frame.width,"height":d.frame.height},"scale":d.scale,"primary":d.primary})).collect();
    let assignment_rows: Vec<_> = displays.iter().enumerate().map(|(n,d)| {
        let a = assignments.and_then(|a| a.displays.get(&d.id).or_else(|| d.legacy_ids.iter().find_map(|id| a.displays.get(id))));
        json!({"display":format!("display-{}",n+1),"assigned":a.is_some_and(|a| a.item.is_some()) || assignments.is_some_and(|a|a.default.is_some()),"paused":a.is_some_and(|a| a.paused)})
    }).collect();
    omissions.add(
        "disconnected-assignments",
        assignments.map_or(0, |a| {
            a.displays
                .keys()
                .filter(|id| {
                    !displays
                        .iter()
                        .any(|d| d.id == **id || d.legacy_ids.contains(id))
                })
                .count() as u64
        }),
    );
    let doc = json!({
        "schema":1,"version":env!("CARGO_PKG_VERSION"),"os":std::env::consts::OS,"arch":std::env::consts::ARCH,
        "backend":wallpaper_platform::Desktop::BACKEND,"generatedAt":wallpaper_core::timestamp(),
        "settings":report.config.value.as_ref().map(settings),
        "store":{"config":state(&report.config),"library":state(&report.library),"assignments":state(&report.assignments)},
        "library":{"total":library.map_or(0, |l| l.items.len()),"byKind":kinds,"byExtension":extensions,"byFit":fits},
        "displays":display_rows,"assignments":{"hasDefault":assignments.is_some_and(|a| a.default.is_some()),"displays":assignment_rows},
        "logs":{"app.log.1":old,"app.log":current},"omissions":omissions.0,
        "note":"No live per-display snapshot is included; state transitions are recorded in logs. Personal names, IDs, paths and raw store details are omitted."
    });
    bounded(doc, EXPORT_LIMIT)
}
fn bounded(mut doc: Value, limit: usize) -> Result<Vec<u8>> {
    loop {
        let bytes = serde_json::to_vec_pretty(&doc)?;
        if bytes.len() <= limit {
            return Ok(bytes);
        }
        let mut excess = bytes.len() - limit;
        let mut removed = 0;
        for key in ["app.log.1", "app.log"] {
            let rows = doc["logs"][key].as_array_mut().context("logs missing")?;
            let mut count = 0;
            for row in rows.iter() {
                if excess == 0 {
                    break;
                }
                excess = excess.saturating_sub(serde_json::to_vec(row)?.len());
                count += 1;
            }
            rows.drain(..count);
            removed += count;
        }
        ensure!(
            removed > 0,
            "required diagnostic summary exceeds size limit"
        );
        let n = doc["omissions"]["export-size-lines"].as_u64().unwrap_or(0);
        doc["omissions"]["export-size-lines"] = json!(n + removed as u64);
    }
}
// Canonicalize the existing ancestor, then append missing components. This also
// catches aliases through symlinked parents when data roots do not yet exist.
fn normalized(path: &Path) -> Result<PathBuf> {
    let absolute = if path.is_absolute() {
        path.to_path_buf()
    } else {
        std::env::current_dir()?.join(path)
    };
    let mut result = PathBuf::new();
    for part in absolute.components() {
        use std::path::Component;
        match part {
            Component::ParentDir => {
                result.pop();
            }
            Component::CurDir => {}
            _ => {
                result.push(part.as_os_str());
                if fs::symlink_metadata(&result).is_ok() {
                    result = result.canonicalize()?;
                }
            }
        }
    }
    Ok(result)
}
fn destination(paths: &Paths, dst: &Path) -> Result<PathBuf> {
    match fs::symlink_metadata(dst) {
        Ok(_) => bail!("diagnostic target already exists"),
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => {}
        Err(e) => return Err(e.into()),
    }
    let parent = dst
        .parent()
        .filter(|p| !p.as_os_str().is_empty())
        .unwrap_or(Path::new("."));
    let parent = parent
        .canonicalize()
        .context("diagnostic target parent must exist")?;
    for root in [&paths.config, &paths.library, &paths.cache, &paths.logs] {
        ensure!(
            !parent.starts_with(normalized(root)?),
            "diagnostic target is inside a data directory"
        );
    }
    Ok(parent.join(dst.file_name().context("target filename missing")?))
}
pub fn export(
    paths: &Paths,
    dst: &Path,
    displays: Option<&[DisplayInfo]>,
    known: &[String],
) -> Result<()> {
    let dst = destination(paths, dst)?;
    let bytes = collect(paths, displays, known)?;
    let temp = dst.parent().unwrap().join(format!(
        ".wallpaper-diagnostic-{}-{}.tmp",
        std::process::id(),
        std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)?
            .as_nanos()
    ));
    let mut file = fs::OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(&temp)?;
    let result = (|| -> Result<()> {
        file.write_all(&bytes)?;
        file.sync_all()?;
        drop(file);
        wallpaper_platform::rename_new(&temp, &dst)?;
        Ok(())
    })();
    if temp.exists() {
        fs::remove_file(&temp)?;
    }
    result
}

#[cfg(test)]
mod tests {
    use super::*;
    fn paths(root: &Path) -> Paths {
        Paths {
            config: root.join("config"),
            library: root.join("library"),
            cache: root.join("cache"),
            logs: root.join("logs"),
        }
    }
    #[test]
    fn display_enumeration_failure_is_reported_separately_from_empty() {
        let paths = paths(&crate::test_temp("display-unavailable"));
        let missing: Value = serde_json::from_slice(&collect(&paths, None, &[]).unwrap()).unwrap();
        let empty: Value =
            serde_json::from_slice(&collect(&paths, Some(&[]), &[]).unwrap()).unwrap();
        assert_eq!(missing["omissions"]["displays-unavailable"], 1);
        assert!(empty["omissions"].get("displays-unavailable").is_none());
        assert_eq!(missing["displays"], json!([]));
    }
    #[test]
    fn thousand_items_and_two_large_logs_are_scrubbed() {
        use wallpaper_core::library::{Fit, Item, Kind, Library};
        let root = crate::test_temp("redactor-scale");
        let paths = paths(&root);
        let items = (0..1000)
            .map(|n| Item {
                id: format!("private-id-{n:04}"),
                title: format!("private-title-{n:04}"),
                entry: format!("private-entry-{n:04}.mp4"),
                kind: Kind::Video,
                fit: Fit::Fill,
                width: None,
                height: None,
                duration_sec: None,
                size_bytes: None,
                added_at: String::new(),
            })
            .collect();
        Store::save_library(
            &paths,
            &Library {
                items,
                ..Library::default()
            },
        )
        .unwrap();
        fs::create_dir_all(&paths.logs).unwrap();
        let mut log = String::new();
        for n in (0..1000).cycle() {
            log.push_str(&format!("failure private-id-{n:04} private-title-{n:04} private-entry-{n:04}.mp4; avf: Cannot Open\n"));
            if log.len() >= 2 * 1024 * 1024 {
                break;
            }
        }
        for name in ["app.log.1", "app.log"] {
            fs::write(paths.logs.join(name), &log).unwrap();
        }
        let bytes = collect(&paths, Some(&[]), &[]).unwrap();
        assert!(bytes.len() <= EXPORT_LIMIT);
        let text = String::from_utf8(bytes.clone()).unwrap();
        assert!(!text.contains("private-id-"));
        assert!(!text.contains("private-title-"));
        assert!(!text.contains("private-entry-"));
        let doc: Value = serde_json::from_slice(&bytes).unwrap();
        assert_eq!(doc["library"]["total"], 1000);
        for name in ["app.log.1", "app.log"] {
            let rows = doc["logs"][name].as_array().unwrap();
            assert!(!rows.is_empty());
            assert!(
                rows.iter()
                    .all(|row| row == "failure [redacted] [redacted] [redacted]; avf: Cannot Open")
            );
        }
        fs::remove_dir_all(root).unwrap();
    }
    #[test]
    fn tail_boundaries_oversize_and_invalid_lines() {
        let root = crate::test_temp("tails");
        fs::create_dir_all(&root).unwrap();
        let path = root.join("app.log");
        let mut bytes = vec![b'x'; TAIL_LIMIT as usize + 10];
        bytes.extend_from_slice(b"\nvalid\npartial");
        fs::write(&path, bytes).unwrap();
        let mut omissions = Omissions::default();
        assert_eq!(tail(&path, &Redactor::default(), &mut omissions), ["valid"]);
        assert_eq!(omissions.0["partial-first-line"], 1);
        assert_eq!(omissions.0["partial-last-line"], 1);
        let mut bytes = vec![b'x'; LINE_LIMIT + 1];
        bytes.extend_from_slice(b"\n\xff\n\"/unclosed\nokay\n");
        fs::write(&path, bytes).unwrap();
        let mut omissions = Omissions::default();
        assert_eq!(tail(&path, &Redactor::default(), &mut omissions), ["okay"]);
        assert_eq!(omissions.0["oversized-line"], 1);
        assert_eq!(omissions.0["invalid-unicode-line"], 1);
        assert_eq!(omissions.0["unsafe-line"], 1);
    }
    #[test]
    fn measures_escaped_final_bytes_and_preserves_summary() {
        let rows: Vec<_> = (0..400).map(|_| "\t".repeat(8192)).collect();
        let doc = json!({"schema":1,"settings":{"required":true},"logs":{"app.log.1":rows,"app.log":["newest"]},"omissions":{},"note":"required summary"});
        let bytes = bounded(doc, EXPORT_LIMIT).unwrap();
        assert!(bytes.len() <= EXPORT_LIMIT);
        let result: Value = serde_json::from_slice(&bytes).unwrap();
        assert_eq!(result["schema"], 1);
        assert_eq!(result["settings"]["required"], true);
        assert_eq!(result["logs"]["app.log"][0], "newest");
        assert!(result["omissions"]["export-size-lines"].as_u64().unwrap() > 0);
    }
    #[test]
    fn whitelist_scrubs_all_private_values_and_keeps_effective_settings() {
        use wallpaper_core::{
            displays::{Assignment, Assignments, DisplayId, Frame},
            library::{Fit, Item, Kind, Library},
        };
        let root = crate::test_temp("privacy");
        let paths = paths(&root);
        let item = Item {
            id: "secret-item-id".into(),
            title: "secret-title".into(),
            entry: "secret-entry.gif".into(),
            kind: Kind::Image,
            fit: Fit::Fit,
            width: Some(10),
            height: Some(10),
            duration_sec: None,
            size_bytes: None,
            added_at: String::new(),
        };
        Store::save_library(
            &paths,
            &Library {
                items: vec![item],
                ..Library::default()
            },
        )
        .unwrap();
        let mut a = Assignments {
            default: Some("secret-default-id".into()),
            ..Assignments::default()
        };
        a.displays.insert(
            DisplayId("secret-display-id".into()),
            Assignment {
                item: Some("secret-reference-id".into()),
                paused: true,
            },
        );
        a.aliases
            .insert(DisplayId("secret-display-id".into()), "secret-alias".into());
        Store::save_assignments(&paths, &a).unwrap();
        let display = DisplayInfo {
            id: DisplayId("secret-display-id".into()),
            legacy_ids: vec![],
            name: "secret-monitor-name".into(),
            primary: true,
            frame: Frame {
                x: 0,
                y: 0,
                width: 10,
                height: 10,
            },
            scale: 2.0,
        };
        fs::create_dir_all(&paths.logs).unwrap();
        let private = [
            "secret-item-id",
            "secret-title",
            "secret-entry.gif",
            "secret-default-id",
            "secret-reference-id",
            "secret-display-id",
            "secret-alias",
            "secret-monitor-name",
        ];
        fs::write(
            paths.logs.join("app.log"),
            format!("{}\n", private.join(" ")),
        )
        .unwrap();
        let bytes = collect(&paths, Some(&[display]), &[]).unwrap();
        let text = String::from_utf8(bytes.clone()).unwrap();
        for value in private {
            assert!(!text.contains(value), "{value}");
        }
        let doc: Value = serde_json::from_slice(&bytes).unwrap();
        assert_eq!(doc["library"]["byExtension"]["gif"], 1);
        assert_eq!(doc["displays"][0]["display"], "display-1");
        assert_eq!(doc["assignments"]["displays"][0]["paused"], true);
        assert_eq!(doc["store"]["config"], "missing");
        assert_eq!(doc["settings"]["startup"]["openSettings"], true);
        assert_eq!(extension("personal.unrecognized"), "other");
    }
    #[test]
    fn export_missing_root_is_read_only_and_rejects_existing_and_data_targets() {
        let root = crate::test_temp("export");
        fs::create_dir_all(&root).unwrap();
        let paths = paths(&root.join("absent"));
        let dst = root.join("export.json");
        export(&paths, &dst, Some(&[]), &[]).unwrap();
        assert!(!root.join("absent").exists());
        let before = fs::read(&dst).unwrap();
        assert!(export(&paths, &dst, Some(&[]), &[]).is_err());
        assert_eq!(fs::read(&dst).unwrap(), before);
        for dir in [&paths.config, &paths.library, &paths.cache, &paths.logs] {
            fs::create_dir_all(dir).unwrap();
            assert!(export(&paths, &dir.join("bad.json"), Some(&[]), &[]).is_err());
            assert_eq!(fs::read_dir(dir).unwrap().count(), 0);
        }
        assert_eq!(fs::read_dir(&root).unwrap().count(), 2);
    }
    #[cfg(unix)]
    #[test]
    fn symlink_logs_and_target_aliases_are_rejected() {
        use std::os::unix::fs::symlink;
        let root = crate::test_temp("symlinks");
        fs::create_dir_all(&root).unwrap();
        let paths = paths(&root.join("data"));
        fs::create_dir_all(&paths.logs).unwrap();
        let secret = root.join("private");
        fs::write(&secret, b"never-read-this\n").unwrap();
        symlink(&secret, paths.logs.join("app.log")).unwrap();
        let bytes = collect(&paths, Some(&[]), &[]).unwrap();
        assert!(!String::from_utf8_lossy(&bytes).contains("never-read-this"));
        let doc: Value = serde_json::from_slice(&bytes).unwrap();
        assert_eq!(doc["omissions"]["log-not-regular"], 1);
        let alias = root.join("alias");
        symlink(&paths.logs, &alias).unwrap();
        assert!(export(&paths, &alias.join("bad.json"), Some(&[]), &[]).is_err());
        assert!(!paths.logs.join("bad.json").exists());
        symlink(root.join("missing"), root.join("dangling")).unwrap();
        assert!(export(&paths, &root.join("dangling"), Some(&[]), &[]).is_err());
        assert_eq!(fs::read(&secret).unwrap(), b"never-read-this\n");
    }
}
