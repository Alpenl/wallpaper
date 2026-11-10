use std::{
    collections::BTreeMap,
    fs,
    path::PathBuf,
    sync::atomic::{AtomicU64, Ordering},
    time::{SystemTime, UNIX_EPOCH},
};
use wallpaper_core::{config::Settings, displays::Assignments, library::Library};
use wallpaper_spike::storage::{Paths, State, Store};

struct Temp {
    root: PathBuf,
    paths: Paths,
}
impl Temp {
    fn new() -> Self {
        static COUNTER: AtomicU64 = AtomicU64::new(0);
        let root = std::env::temp_dir().join(format!(
            "wp-storage-{}-{}-{}",
            std::process::id(),
            SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .unwrap()
                .as_nanos(),
            COUNTER.fetch_add(1, Ordering::Relaxed)
        ));
        fs::create_dir_all(&root).unwrap();
        let paths = Paths {
            config: root.join("config"),
            library: root.join("library"),
            cache: root.join("cache"),
            logs: root.join("logs"),
        };
        Self { root, paths }
    }
    fn write(&self, name: &str, bytes: &[u8]) {
        fs::create_dir_all(&self.paths.config).unwrap();
        fs::write(self.paths.config.join(name), bytes).unwrap();
    }
    fn snapshot(&self) -> BTreeMap<String, Vec<u8>> {
        if !self.paths.config.exists() {
            return BTreeMap::new();
        }
        fs::read_dir(&self.paths.config)
            .unwrap()
            .map(|entry| {
                let entry = entry.unwrap();
                (
                    entry.file_name().to_str().unwrap().to_owned(),
                    fs::read(entry.path()).unwrap(),
                )
            })
            .collect()
    }
}
impl Drop for Temp {
    fn drop(&mut self) {
        fs::remove_dir_all(&self.root).unwrap();
    }
}

fn samples() -> [(&'static str, Vec<u8>); 3] {
    [
        (
            "config.json",
            serde_json::to_vec(&Settings::default()).unwrap(),
        ),
        (
            "library.json",
            serde_json::to_vec(&Library::default()).unwrap(),
        ),
        (
            "assignments.json",
            serde_json::to_vec(&Assignments::default()).unwrap(),
        ),
    ]
}

#[test]
fn main_read_errors_leave_directories_and_valid_backups_unchanged() {
    let temp = Temp::new();
    for (name, bytes) in samples() {
        let directory = temp.paths.config.join(name);
        fs::create_dir_all(&directory).unwrap();
        fs::write(directory.join("sentinel"), b"preserve directory contents").unwrap();
        temp.write(&format!("{name}.bak"), &bytes);
    }

    let report = Store::open(&temp.paths);
    for (state, detail, preserved) in [
        (
            report.config.state,
            report.config.detail,
            report.config.preserved_path,
        ),
        (
            report.library.state,
            report.library.detail,
            report.library.preserved_path,
        ),
        (
            report.assignments.state,
            report.assignments.detail,
            report.assignments.preserved_path,
        ),
    ] {
        assert_eq!(state, State::Corrupt);
        assert!(detail.contains("main file read failed:"));
        assert!(preserved.is_none());
    }
    for (name, bytes) in samples() {
        let directory = temp.paths.config.join(name);
        assert!(directory.is_dir());
        assert_eq!(fs::read_dir(&directory).unwrap().count(), 1);
        assert_eq!(
            fs::read(directory.join("sentinel")).unwrap(),
            b"preserve directory contents"
        );
        assert_eq!(
            fs::read(temp.paths.config.join(format!("{name}.bak"))).unwrap(),
            bytes
        );
    }
    assert_eq!(fs::read_dir(&temp.paths.config).unwrap().count(), 6);
}

#[test]
fn missing_defaults_do_not_create_directories_or_files() {
    let temp = Temp::new();
    let report = Store::open(&temp.paths);
    assert_eq!(report.config.state, State::Missing);
    assert_eq!(report.library.state, State::Missing);
    assert_eq!(report.assignments.state, State::Missing);
    assert_eq!(report.config.value.unwrap(), Settings::default());
    assert_eq!(report.library.value.unwrap(), Library::default());
    assert_eq!(report.assignments.value.unwrap(), Assignments::default());
    assert_eq!(fs::read_dir(&temp.root).unwrap().count(), 0);
}

#[test]
fn loaded_files_ignore_backup_and_normalize_settings_without_writes() {
    let temp = Temp::new();
    for (name, bytes) in samples() {
        temp.write(name, &bytes);
        temp.write(&format!("{name}.bak"), b"invalid");
    }
    let mut settings = Settings::default();
    settings.pause.low_battery_pct = 101;
    settings.pause.resume_debounce_ms = 0;
    temp.write("config.json", &serde_json::to_vec(&settings).unwrap());
    let before = temp.snapshot();
    let report = Store::open(&temp.paths);
    assert_eq!(report.config.state, State::Loaded);
    assert_eq!(report.library.state, State::Loaded);
    assert_eq!(report.assignments.state, State::Loaded);
    assert_eq!(report.config.value.as_ref().unwrap(), &Settings::default());
    assert!(!report.has_errors());
    assert_eq!(temp.snapshot(), before);
}

#[test]
fn every_invalid_main_kind_recovers_from_backup_and_preserves_original() {
    for invalid in [
        b"{".as_slice(),
        b"{}",
        br#"{"schemaVersion":0}"#,
        br#"{"schemaVersion":1}"#,
        br#"{"schemaVersion":"1"}"#,
    ] {
        let temp = Temp::new();
        for (name, bytes) in samples() {
            temp.write(name, invalid);
            temp.write(&format!("{name}.bak"), &bytes);
        }
        let report = Store::open(&temp.paths);
        for (state, preserved) in [
            (report.config.state, report.config.preserved_path),
            (report.library.state, report.library.preserved_path),
            (report.assignments.state, report.assignments.preserved_path),
        ] {
            assert_eq!(state, State::Recovered);
            let preserved = preserved.unwrap();
            assert!(
                preserved
                    .file_name()
                    .unwrap()
                    .to_str()
                    .unwrap()
                    .contains(".corrupt-")
            );
            assert_eq!(fs::read(preserved).unwrap(), invalid);
        }
        for (name, bytes) in samples() {
            assert_eq!(fs::read(temp.paths.config.join(name)).unwrap(), bytes);
            assert_eq!(
                fs::read(temp.paths.config.join(format!("{name}.bak"))).unwrap(),
                bytes
            );
        }
        assert_eq!(temp.snapshot().len(), 9);
    }
}

#[test]
fn missing_main_recovers_without_preserved_path() {
    let temp = Temp::new();
    for (name, bytes) in samples() {
        temp.write(&format!("{name}.bak"), &bytes);
    }
    let report = Store::open(&temp.paths);
    for (state, preserved) in [
        (report.config.state, report.config.preserved_path),
        (report.library.state, report.library.preserved_path),
        (report.assignments.state, report.assignments.preserved_path),
    ] {
        assert_eq!(state, State::Recovered);
        assert!(preserved.is_none());
    }
    assert_eq!(temp.snapshot().len(), 6);
    for (name, bytes) in samples() {
        assert_eq!(fs::read(temp.paths.config.join(name)).unwrap(), bytes);
    }
}

#[test]
fn corrupt_combinations_leave_directory_byte_identical() {
    for (main, backup) in [
        (Some(b"{".as_slice()), Some(b"bad".as_slice())),
        (None, Some(b"bad".as_slice())),
        (Some(b"bad".as_slice()), None),
        (
            Some(b"{}".as_slice()),
            Some(br#"{"schemaVersion":2}"#.as_slice()),
        ),
    ] {
        let temp = Temp::new();
        for (name, _) in samples() {
            if let Some(bytes) = main {
                temp.write(name, bytes);
            }
            if let Some(bytes) = backup {
                temp.write(&format!("{name}.bak"), bytes);
            }
        }
        let before = temp.snapshot();
        let report = Store::open(&temp.paths);
        assert!(report.has_errors());
        for (state, detail) in [
            (report.config.state, report.config.detail),
            (report.library.state, report.library.detail),
            (report.assignments.state, report.assignments.detail),
        ] {
            assert_eq!(state, State::Corrupt);
            assert!(detail.contains("main:"));
            assert!(detail.contains("backup:"));
        }
        assert_eq!(temp.snapshot(), before);
    }
}

#[test]
fn newer_main_ignores_valid_or_invalid_backup_without_mutations() {
    for valid_backup in [true, false] {
        let temp = Temp::new();
        for (name, bytes) in samples() {
            temp.write(name, br#"{"schemaVersion":2,"future":"synthetic"}"#);
            temp.write(
                &format!("{name}.bak"),
                if valid_backup { &bytes } else { b"bad" },
            );
        }
        let before = temp.snapshot();
        let report = Store::open(&temp.paths);
        assert!(report.has_errors());
        assert_eq!(report.config.state, State::Newer);
        assert_eq!(report.library.state, State::Newer);
        assert_eq!(report.assignments.state, State::Newer);
        assert!(report.config.value.is_none());
        assert!(report.library.value.is_none());
        assert!(report.assignments.value.is_none());
        assert_eq!(temp.snapshot(), before);
    }
}

#[test]
fn unknown_new_enums_are_corrupt_and_do_not_modify_files() {
    let temp = Temp::new();
    let mut settings = serde_json::to_value(Settings::default()).unwrap();
    settings["ui"]["theme"] = serde_json::json!("unknown");
    temp.write("config.json", settings.to_string().as_bytes());
    let before = temp.snapshot();
    assert_eq!(Store::open(&temp.paths).config.state, State::Corrupt);
    assert_eq!(temp.snapshot(), before);
}

#[test]
fn saves_use_pretty_newline_and_backup_exact_previous_bytes() {
    let temp = Temp::new();
    Store::save_config(&temp.paths, &Settings::default()).unwrap();
    Store::save_library(&temp.paths, &Library::default()).unwrap();
    Store::save_assignments(&temp.paths, &Assignments::default()).unwrap();
    assert_eq!(temp.snapshot().len(), 3);
    for (name, _) in samples() {
        let data = fs::read(temp.paths.config.join(name)).unwrap();
        assert!(data.ends_with(b"\n"));
        assert!(data.windows(3).any(|part| part == b"  \""));
        temp.write(name, &[b" \n".as_slice(), &data].concat());
    }
    let previous = temp.snapshot();
    let mut settings = Settings::default();
    settings.startup.launch_at_login = true;
    Store::save_config(&temp.paths, &settings).unwrap();
    Store::save_library(&temp.paths, &Library::default()).unwrap();
    Store::save_assignments(&temp.paths, &Assignments::default()).unwrap();
    for (name, bytes) in &previous {
        assert_eq!(
            fs::read(temp.paths.config.join(format!("{name}.bak"))).unwrap(),
            *bytes
        );
    }
    assert_eq!(Store::open(&temp.paths).config.value.unwrap(), settings);
    assert_eq!(temp.snapshot().len(), 6);
    let second = fs::read(temp.paths.config.join("config.json")).unwrap();
    Store::save_config(&temp.paths, &Settings::default()).unwrap();
    assert_eq!(
        fs::read(temp.paths.config.join("config.json.bak")).unwrap(),
        second
    );
}

#[test]
fn reset_only_renames_all_existing_main_and_backup_files() {
    let temp = Temp::new();
    assert!(Store::reset(&temp.paths).unwrap().is_empty());
    for (name, bytes) in samples() {
        temp.write(name, &bytes);
        temp.write(&format!("{name}.bak"), b"synthetic backup");
    }
    temp.write("unrelated", b"preserve");
    let before = temp.snapshot();
    let moved = Store::reset(&temp.paths).unwrap();
    assert_eq!(moved.len(), 6);
    for path in moved {
        let name = path.file_name().unwrap().to_str().unwrap();
        let (original, nanos) = name.split_once(".reset-").unwrap();
        assert!(nanos.parse::<u128>().is_ok());
        assert_eq!(fs::read(&path).unwrap(), before[original]);
        assert!(!temp.paths.config.join(original).exists());
    }
    assert_eq!(
        fs::read(temp.paths.config.join("unrelated")).unwrap(),
        b"preserve"
    );
    assert_eq!(temp.snapshot().len(), 7);
    assert!(Store::reset(&temp.paths).unwrap().is_empty());
}

#[test]
fn startup_inspection_never_writes_even_with_recovery_and_another_file_error() {
    let temp = Temp::new();
    assert!(!Store::inspect(&temp.paths).has_errors());
    assert!(!temp.paths.config.exists());
    for (name, bytes) in samples() {
        temp.write(name, b"corrupt");
        temp.write(&format!("{name}.bak"), &bytes);
    }
    let before = temp.snapshot();
    let report = Store::inspect(&temp.paths);
    assert!(!report.has_errors());
    assert_eq!(report.config.state, State::Recovered);
    assert_eq!(report.library.state, State::Recovered);
    assert_eq!(report.assignments.state, State::Recovered);
    assert_eq!(report.library.value.unwrap(), Library::default());
    assert_eq!(temp.snapshot(), before);
    for name in ["config.json", "library.json", "assignments.json"] {
        let original = temp.snapshot();
        temp.write(name, br#"{"schemaVersion":999}"#);
        let before = temp.snapshot();
        assert!(Store::inspect(&temp.paths).has_errors());
        assert_eq!(temp.snapshot(), before);
        temp.write(name, &original[name]);
        temp.write(&format!("{name}.bak"), b"corrupt too");
        let before = temp.snapshot();
        assert!(Store::inspect(&temp.paths).has_errors());
        assert_eq!(temp.snapshot(), before);
        for (file, bytes) in original {
            temp.write(&file, &bytes);
        }
    }
}
