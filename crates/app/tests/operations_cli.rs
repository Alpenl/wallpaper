use serde_json::Value;
use std::{
    fs,
    path::PathBuf,
    process::{Command, Output},
    sync::atomic::{AtomicU64, Ordering},
};
struct Fixture {
    root: PathBuf,
    home: PathBuf,
}
impl Fixture {
    fn new() -> Self {
        static NEXT: AtomicU64 = AtomicU64::new(0);
        let root = std::env::temp_dir().join(format!(
            "wp-4b-cli-{}-{}",
            std::process::id(),
            NEXT.fetch_add(1, Ordering::Relaxed)
        ));
        fs::create_dir_all(&root).unwrap();
        Self {
            home: root.join("home"),
            root,
        }
    }
    fn run(&self, args: &[&str]) -> Output {
        Command::new(env!("CARGO_BIN_EXE_wallpaper-spike"))
            .env("WPAPP_HOME", &self.home)
            .env("XDG_RUNTIME_DIR", &self.root)
            .args(args)
            .output()
            .unwrap()
    }
}
impl Drop for Fixture {
    fn drop(&mut self) {
        let _ = fs::remove_dir_all(&self.root);
    }
}
fn json(output: &Output) -> Value {
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    serde_json::from_slice(&output.stdout).unwrap()
}
#[test]
fn status_and_export_create_no_home_or_lock() {
    let f = Fixture::new();
    let status = json(&f.run(&["--launch-at-login", "status"]));
    assert_eq!(status["backend"], "isolated");
    assert!(!f.home.exists());
    let dst = f.root.join("diagnostics.json");
    let output = f.run(&["--export-diagnostics", dst.to_str().unwrap()]);
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    assert!(!f.home.exists());
    let bytes = fs::read(&dst).unwrap();
    assert_eq!(
        f.run(&["--export-diagnostics", dst.to_str().unwrap()])
            .status
            .code(),
        Some(1)
    );
    assert_eq!(fs::read(&dst).unwrap(), bytes);
}
#[test]
fn on_off_preflight_and_secondary_does_not_activate() {
    let f = Fixture::new();
    for state in ["on", "on", "off", "off"] {
        assert!(f.run(&["--launch-at-login", state]).status.success());
        assert_eq!(
            json(&f.run(&["--launch-at-login", "status"]))["registered"],
            state == "on"
        );
    }
    let config = f.home.join("v2/config");
    let before = fs::read(config.join("config.json")).unwrap();
    let lock = fs::OpenOptions::new()
        .write(true)
        .open(config.join("instance.lock"))
        .unwrap();
    lock.lock().unwrap();
    assert_eq!(f.run(&["--launch-at-login", "on"]).status.code(), Some(4));
    assert!(!config.join("activate").exists());
    assert_eq!(fs::read(config.join("config.json")).unwrap(), before);
    let dst = f.root.join("locked-export.json");
    assert!(
        f.run(&["--export-diagnostics", dst.to_str().unwrap()])
            .status
            .success()
    );
    assert_eq!(fs::read(config.join("config.json")).unwrap(), before);
    assert!(!config.join("activate").exists());
    drop(lock);
    for bad in [b"bad".as_slice(), b"{\"schemaVersion\":999}"] {
        fs::write(config.join("config.json"), bad).unwrap();
        let _ = fs::remove_file(config.join("config.json.bak"));
        assert_eq!(f.run(&["--launch-at-login", "on"]).status.code(), Some(2));
        assert_eq!(fs::read(config.join("config.json")).unwrap(), bad);
        assert_eq!(
            json(&f.run(&["--launch-at-login", "status"]))["registered"],
            false
        );
    }
}

#[test]
fn export_preserves_all_data_files_with_active_lock() {
    fn inventory(root: &std::path::Path) -> Vec<(PathBuf, Vec<u8>)> {
        let mut rows = Vec::new();
        for entry in fs::read_dir(root).unwrap() {
            let path = entry.unwrap().path();
            if path.is_dir() {
                rows.extend(inventory(&path));
            } else {
                rows.push((path.clone(), fs::read(path).unwrap()));
            }
        }
        rows.sort();
        rows
    }
    let f = Fixture::new();
    assert!(f.run(&["--launch-at-login", "on"]).status.success());
    let config = f.home.join("v2/config");
    fs::write(config.join("library.json"), b"corrupt-store-secret").unwrap();
    fs::create_dir_all(f.home.join("v2/logs")).unwrap();
    fs::write(f.home.join("v2/logs/app.log"), b"safe event\n").unwrap();
    let lock = fs::OpenOptions::new()
        .write(true)
        .open(config.join("instance.lock"))
        .unwrap();
    lock.lock().unwrap();
    let before = inventory(&f.home);
    let dst = f.root.join("preserved.json");
    assert!(
        f.run(&["--export-diagnostics", dst.to_str().unwrap()])
            .status
            .success()
    );
    assert_eq!(before, inventory(&f.home));
    let doc: Value = serde_json::from_slice(&fs::read(&dst).unwrap()).unwrap();
    assert_eq!(doc["store"]["library"], "corrupt");
    assert!(
        !String::from_utf8(fs::read(&dst).unwrap())
            .unwrap()
            .contains("corrupt-store-secret")
    );
}
