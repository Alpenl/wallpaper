#![cfg(unix)]
use serde_json::Value;
use std::{
    fs,
    path::{Path, PathBuf},
    process::{Command, Output, Stdio},
    sync::atomic::{AtomicU64, Ordering},
    time::{Duration, Instant},
};
struct Fixture(PathBuf);
impl Fixture {
    fn new() -> Self {
        static SERIAL: AtomicU64 = AtomicU64::new(0);
        // Short enough for the isolated legacy socket candidates on macOS.
        let root = PathBuf::from("/private/tmp").join(format!(
            "wp-cli-{}-{}",
            std::process::id(),
            SERIAL.fetch_add(1, Ordering::Relaxed)
        ));
        fs::create_dir(&root).unwrap();
        Self(root)
    }
    fn command(&self) -> Command {
        let mut command = Command::new(env!("CARGO_BIN_EXE_wallpaper-spike"));
        command
            .env("WPAPP_HOME", &self.0)
            .env("XDG_RUNTIME_DIR", &self.0);
        command
    }
    fn run(&self, args: &[&str]) -> Output {
        let output = self.command().args(args).output().unwrap();
        assert!(
            output.status.code().is_some(),
            "crashed: {}",
            String::from_utf8_lossy(&output.stderr)
        );
        output
    }
    fn source(&self, name: &str) -> PathBuf {
        // Minimal uncompressed TIFF image, intentionally named PNG: extension is
        // admission policy; the actual bytes are decoded by ImageIO, not trusted.
        let path = self.0.join(name);
        let entries: [(u16, u16, u32, u32); 9] = [
            (256, 4, 1, 2),
            (257, 4, 1, 1),
            (258, 3, 3, 122),
            (259, 3, 1, 1),
            (262, 3, 1, 2),
            (273, 4, 1, 128),
            (277, 3, 1, 3),
            (278, 4, 1, 1),
            (279, 4, 1, 6),
        ];
        let mut bytes = b"II\x2a\x00\x08\x00\x00\x00".to_vec();
        bytes.extend_from_slice(&9u16.to_le_bytes());
        for (tag, kind, count, value) in entries {
            bytes.extend_from_slice(&tag.to_le_bytes());
            bytes.extend_from_slice(&kind.to_le_bytes());
            bytes.extend_from_slice(&count.to_le_bytes());
            bytes.extend_from_slice(&value.to_le_bytes());
        }
        bytes.extend_from_slice(&[0; 4]);
        bytes.extend_from_slice(&[8, 0, 8, 0, 8, 0]);
        bytes.extend_from_slice(&[255, 0, 0, 0, 255, 0]);
        fs::write(&path, bytes).unwrap();
        path
    }
}
impl Drop for Fixture {
    fn drop(&mut self) {
        let _ = fs::remove_dir_all(&self.0);
    }
}
fn json(output: &Output) -> Value {
    serde_json::from_slice(&output.stdout).unwrap()
}
#[test]
fn hidden_probe_bypasses_store_lock_and_rejects_bad_arguments() {
    let f = Fixture::new();
    let source = f.source("雪 山.png");
    fs::create_dir_all(f.0.join("v2/config")).unwrap();
    fs::write(f.0.join("v2/config/library.json"), b"corrupt").unwrap();
    let lock = fs::File::create(f.0.join("v2/config/instance.lock")).unwrap();
    lock.lock().unwrap();
    let thumb = f.0.join("thumb.jpg");
    let output = f.run(&[
        "--media-probe",
        source.to_str().unwrap(),
        "--kind",
        "image",
        "--thumb",
        thumb.to_str().unwrap(),
    ]);
    assert_eq!(
        output.status.code(),
        Some(0),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    assert_eq!(json(&output)["ok"]["width"], 2);
    assert_eq!(output.stdout.iter().filter(|c| **c == b'\n').count(), 1);
    assert!(thumb.is_file());
    assert_eq!(
        fs::read(f.0.join("v2/config/library.json")).unwrap(),
        b"corrupt"
    );
    let output = f.run(&[
        "--media-probe",
        source.to_str().unwrap(),
        "--kind",
        "invalid",
    ]);
    assert_eq!(output.status.code(), Some(3));
    assert!(json(&output).get("rejected").is_some());
    assert!(!f.0.join("v2/config/activate").exists());
}
#[test]
fn import_search_assign_fit_confirmation_delete_and_exit_codes() {
    let f = Fixture::new();
    let source = f.source("雪 山.PNG");
    let bad = f.0.join("bad.txt");
    fs::write(&bad, b"unsupported").unwrap();
    let output = f.run(&["--import", bad.to_str().unwrap(), source.to_str().unwrap()]);
    assert_eq!(
        output.status.code(),
        Some(6),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    let events: Vec<Value> = String::from_utf8(output.stdout)
        .unwrap()
        .lines()
        .map(|line| serde_json::from_str(line).unwrap())
        .collect();
    assert_eq!(events.last().unwrap()["imported"], 1);
    assert_eq!(events.last().unwrap()["rejected"], 1);
    let output = f.run(&["--library-list", "--search", "雪"]);
    assert!(output.status.success());
    let value = json(&output);
    let item = &value[0];
    let id = item["id"].as_str().unwrap();
    assert!(Path::new(item["thumbnail"].as_str().unwrap()).is_file());
    assert!(f.run(&["--assign", id, "--all"]).status.success());
    assert!(f.run(&["--set-fit", id, "fit"]).status.success());
    let before = fs::read(f.0.join("v2/config/library.json")).unwrap();
    let assignments = fs::read(f.0.join("v2/config/assignments.json")).unwrap();
    let output = f.run(&["--delete", id]);
    assert_eq!(output.status.code(), Some(5));
    assert_eq!(json(&output)["affects_default"], true);
    assert_eq!(
        fs::read(f.0.join("v2/config/library.json")).unwrap(),
        before
    );
    assert_eq!(
        fs::read(f.0.join("v2/config/assignments.json")).unwrap(),
        assignments
    );
    assert!(f.0.join("v2/library").join(id).is_dir());
    assert!(f.run(&["--delete", id, "--yes"]).status.success());
    assert_eq!(json(&f.run(&["--library-list"])), serde_json::json!([]));
    assert!(source.is_file());
    assert_eq!(f.run(&["--delete", id, "--yes"]).status.code(), Some(1));
    fs::write(f.0.join("v2/config/config.json"), b"broken").unwrap();
    assert_eq!(f.run(&["--library-list"]).status.code(), Some(2));
}
#[test]
fn secondary_library_modes_exit_four() {
    let f = Fixture::new();
    fs::create_dir_all(f.0.join("v2/config")).unwrap();
    let lock = fs::File::create(f.0.join("v2/config/instance.lock")).unwrap();
    lock.lock().unwrap();
    for args in [
        vec!["--library-list"],
        vec!["--legacy-import", "--from", f.0.to_str().unwrap()],
        vec!["--delete", "one"],
        vec!["--import", "missing.png"],
        vec!["--assign", "one", "--all"],
        vec!["--set-fit", "one", "fit"],
        vec!["--displays"],
    ] {
        assert_eq!(f.run(&args).status.code(), Some(4));
    }
}
#[test]
fn sigterm_import_drains_finished_and_exits_seven() {
    let f = Fixture::new();
    let source = f.0.join("large.png");
    fs::File::create(&source)
        .unwrap()
        .set_len(2 * 1024 * 1024 * 1024)
        .unwrap();
    let mut child = f
        .command()
        .arg("--import")
        .arg(&source)
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .unwrap();
    let deadline = Instant::now() + Duration::from_secs(10);
    while !f.0.join("v2/library").exists() {
        assert!(Instant::now() < deadline);
        assert!(child.try_wait().unwrap().is_none());
        std::thread::sleep(Duration::from_millis(5));
    }
    assert!(
        Command::new("/bin/kill")
            .arg("-TERM")
            .arg(child.id().to_string())
            .status()
            .unwrap()
            .success()
    );
    let output = child.wait_with_output().unwrap();
    assert_eq!(
        output.status.code(),
        Some(7),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    let events: Vec<Value> = String::from_utf8(output.stdout)
        .unwrap()
        .lines()
        .map(|line| serde_json::from_str(line).unwrap())
        .collect();
    assert_eq!(events.last().unwrap()["event"], "finished");
    assert_eq!(events.last().unwrap()["cancelled"], true);
    assert_eq!(fs::read_dir(f.0.join("v2/library")).unwrap().count(), 0);
}

#[test]
fn legacy_import_cli_success_rejection_missing_and_overrides() {
    let f = Fixture::new();
    let config = f.0.join("old-config");
    let library = f.0.join("old-media");
    fs::create_dir_all(&config).unwrap();
    fs::create_dir_all(library.join("one")).unwrap();
    fs::copy(f.source("valid.png"), library.join("one/input.png")).unwrap();
    let manifest = serde_json::json!({"schemaVersion":4,"items":[{"id":"one","type":"image","entry":"input.png","title":"legacy title","props":{"fit":"stretch"}}]});
    fs::write(
        config.join("library.json"),
        serde_json::to_vec(&manifest).unwrap(),
    )
    .unwrap();
    let args = [
        "--legacy-import",
        "--from",
        config.to_str().unwrap(),
        "--from-library",
        library.to_str().unwrap(),
    ];
    let output = f.run(&args);
    assert_eq!(
        output.status.code(),
        Some(0),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    let events: Vec<Value> = String::from_utf8(output.stdout)
        .unwrap()
        .lines()
        .map(|line| serde_json::from_str(line).unwrap())
        .collect();
    assert_eq!(events.last().unwrap()["imported"], 1);
    assert_eq!(json(&f.run(&["--library-list"]))[0]["fit"], "stretch");
    assert_eq!(f.run(&args).status.code(), Some(0));
    fs::write(f.0.join("v2/library/one/media.png"), b"size mismatch").unwrap();
    assert_eq!(f.run(&args).status.code(), Some(6));
    fs::write(config.join("library.json"), b"broken").unwrap();
    assert_eq!(f.run(&args).status.code(), Some(8));
    fs::remove_file(config.join("library.json")).unwrap();
    assert_eq!(f.run(&args).status.code(), Some(8));
    // --from alone defaults the media root to <from>/library.
    let second = Fixture::new();
    let config = second.0.join("old");
    fs::create_dir_all(config.join("library/one")).unwrap();
    fs::copy(
        second.source("valid.png"),
        config.join("library/one/input.png"),
    )
    .unwrap();
    fs::write(
        config.join("library.json"),
        serde_json::to_vec(&manifest).unwrap(),
    )
    .unwrap();
    assert_eq!(
        second
            .run(&["--legacy-import", "--from", config.to_str().unwrap()])
            .status
            .code(),
        Some(0)
    );
}

#[test]
fn legacy_corrupt_store_reports_reason_without_creating_lock() {
    let f = Fixture::new();
    let config = f.0.join("v2/config");
    fs::create_dir_all(&config).unwrap();
    fs::write(config.join("library.json"), b"broken").unwrap();
    let output = f.run(&[
        "--legacy-import",
        "--from",
        f.0.join("missing-old").to_str().unwrap(),
    ]);
    assert_eq!(output.status.code(), Some(2));
    let stderr = String::from_utf8(output.stderr).unwrap();
    assert!(
        stderr.contains("library.json")
            && stderr.contains("corrupt")
            && stderr.contains("--store-check")
    );
    assert_eq!(fs::read_dir(&config).unwrap().count(), 1);
    let output = f.run(&["--library-list"]);
    assert_eq!(output.status.code(), Some(2));
    let stderr = String::from_utf8(output.stderr).unwrap();
    assert!(
        stderr.contains("library.json")
            && stderr.contains("corrupt")
            && stderr.contains("--store-check")
    );
}

#[test]
fn signals_probe_does_not_initialize_storage_or_read_fake_signals() {
    let f = Fixture::new();
    let home = f.0.join("must-not-exist");
    let output = f
        .command()
        .env("WPAPP_HOME", &home)
        .env("WPAPP_FAKE_SIGNALS", f.0.join("missing.json"))
        .arg("--signals-probe")
        .output()
        .unwrap();
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    assert!(!home.exists());
    let value = json(&output);
    assert!(value.get("power").is_some());
    assert!(value["locked"].is_boolean());
    assert!(value["asleep"].is_boolean());
    assert!(value["elapsedMs"].is_number());
    for display in value["displays"].as_object().unwrap().values() {
        for key in ["fullscreen", "covered"] {
            assert!(["yes", "no", "unknown"].contains(&display[key].as_str().unwrap()));
        }
        assert!(display.get("coverage").is_some());
    }
    assert!(value.get("syntheticSignals").is_none());
}
