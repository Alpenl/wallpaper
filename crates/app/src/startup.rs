use crate::storage::{Paths, State, Store};
use anyhow::{Result, bail};
use std::path::Path;
use wallpaper_platform::startup::Backend;

pub fn status(paths: &Paths, backend: &Backend, exe: &Path) -> Result<serde_json::Value> {
    let config = Store::inspect(paths).config;
    let status = backend.status(exe)?;
    Ok(serde_json::json!({
        "backend": backend.name(), "configEnabled": config.value.map(|c| c.startup.launch_at_login),
        "configState": config.state, "registered": status.registered,
        "pathMatches": status.path_matches, "systemState": status.system_state,
    }))
}

pub fn set(paths: &Paths, backend: &Backend, exe: &Path, enabled: bool) -> Result<i32> {
    set_with(
        paths,
        backend,
        exe,
        enabled,
        |settings| Store::save_launch_at_login(paths, settings.startup.launch_at_login),
        |before| backend.restore(before),
    )
}
fn set_with(
    paths: &Paths,
    backend: &Backend,
    exe: &Path,
    enabled: bool,
    save: impl FnOnce(&wallpaper_core::config::Settings) -> Result<()>,
    restore: impl FnOnce(Option<&wallpaper_platform::startup::Registration>) -> Result<()>,
) -> Result<i32> {
    let config = Store::inspect(paths).config;
    if matches!(config.state, State::Corrupt | State::Newer) {
        eprintln!("configuration preflight failed: {:?}", config.state);
        return Ok(2);
    }
    let mut settings = config.value.unwrap_or_default();
    let before = backend.read()?;
    // Restore the exact previous bytes/type, including an absent registration.
    let result = backend.set(enabled, exe).and_then(|()| {
        settings.startup.launch_at_login = enabled;
        save(&settings)
    });
    if let Err(error) = result {
        if let Err(rollback) = restore(before.as_ref()) {
            bail!("launch-at-login failed: {error:#}; rollback failed: {rollback:#}");
        }
        bail!("launch-at-login failed: {error:#}; registration restored");
    }
    Ok(0)
}

pub fn should_open_settings(play: bool, settings: &wallpaper_core::config::Settings) -> bool {
    play || settings.startup.open_settings
}

#[cfg(test)]
mod tests {
    use super::*;
    use wallpaper_platform::startup::{Os, Registration};
    fn fixture(os: Os) -> (Paths, Backend, std::path::PathBuf) {
        let root = crate::test_temp("startup");
        let paths = Paths {
            config: root.join("v2/config"),
            library: root.join("v2/library"),
            cache: root.join("v2/cache"),
            logs: root.join("v2/logs"),
        };
        (paths, Backend::isolated(os, &root), root)
    }
    #[test]
    fn isolated_formats_status_idempotency_and_preservation() {
        for os in [Os::Mac, Os::Windows] {
            let (paths, backend, root) = fixture(os);
            let exe = root.join("a & < > \" ' app");
            let state = status(&paths, &backend, &exe).unwrap();
            assert_eq!(state["backend"], "isolated");
            assert_eq!(state["registered"], false);
            assert_eq!(state["systemState"], "unknown");
            assert!(!root.exists());
            let mut cfg = wallpaper_core::config::Settings::default();
            cfg.ui.theme = wallpaper_core::config::Theme::Light;
            cfg.startup.open_settings = false;
            cfg.pause.on_covered = false;
            Store::save_config(&paths, &cfg).unwrap();
            for enabled in [true, true, false, false] {
                assert_eq!(set(&paths, &backend, &exe, enabled).unwrap(), 0);
                let now = Store::inspect(&paths).config.value.unwrap();
                cfg.startup.launch_at_login = enabled;
                assert_eq!(now, cfg);
                let state = status(&paths, &backend, &exe).unwrap();
                assert_eq!(state["registered"], enabled);
                assert_eq!(state["configEnabled"], enabled);
                if enabled {
                    let before = backend.read().unwrap();
                    assert_eq!(
                        status(&paths, &backend, &root.join("other")).unwrap()["pathMatches"],
                        false
                    );
                    assert_eq!(set(&paths, &backend, &root.join("other"), true).unwrap(), 0);
                    assert_eq!(before, backend.read().unwrap());
                    let text = String::from_utf8(before.unwrap().bytes).unwrap();
                    if os == Os::Mac {
                        assert!(text.contains("&amp; &lt; &gt; &quot; &apos;"));
                        assert!(!text.contains("KeepAlive"));
                        assert!(text.contains("<key>RunAtLoad</key><true/>"));
                    } else {
                        assert_eq!(text, format!("\"{}\"", exe.display()));
                    }
                }
            }
        }
    }
    #[test]
    fn corrupt_newer_and_exact_rollback() {
        for os in [Os::Mac, Os::Windows] {
            let (paths, backend, root) = fixture(os);
            let exe = root.join("exe");
            let original = Registration {
                kind: 1,
                bytes: b"original noncanonical contents\n".to_vec(),
            };
            backend.restore(Some(&original)).unwrap();
            std::fs::create_dir_all(&paths.config).unwrap();
            for bad in [b"invalid".as_slice(), b"{\"schemaVersion\":999}"] {
                std::fs::write(paths.config.join("config.json"), bad).unwrap();
                assert_eq!(set(&paths, &backend, &exe, true).unwrap(), 2);
                assert_eq!(backend.read().unwrap(), Some(original.clone()));
                assert_eq!(
                    std::fs::read(paths.config.join("config.json")).unwrap(),
                    bad
                );
            }
            std::fs::remove_file(paths.config.join("config.json")).unwrap();
            for before in [Some(original.clone()), None] {
                backend.restore(before.as_ref()).unwrap();
                let result = set_with(
                    &paths,
                    &backend,
                    &exe,
                    before.is_none(),
                    |_| anyhow::bail!("injected configuration failure"),
                    |v| backend.restore(v),
                );
                assert!(
                    result
                        .unwrap_err()
                        .to_string()
                        .contains("registration restored")
                );
                assert_eq!(backend.read().unwrap(), before);
            }
            let result = set_with(
                &paths,
                &backend,
                &exe,
                true,
                |_| anyhow::bail!("write failure"),
                |_| anyhow::bail!("injected restore failure"),
            );
            assert!(result.unwrap_err().to_string().contains("rollback failed"));
        }
    }
    #[test]
    fn real_config_write_failure_restores_bytes_or_absence() {
        for before in [
            Some(wallpaper_platform::startup::Registration {
                kind: 1,
                bytes: b"original plist bytes".to_vec(),
            }),
            None,
        ] {
            let (paths, backend, root) = fixture(Os::Mac);
            let cfg = wallpaper_core::config::Settings::default();
            Store::save_config(&paths, &cfg).unwrap();
            let config_before = std::fs::read(paths.config.join("config.json")).unwrap();
            std::fs::create_dir(paths.config.join("config.json.bak")).unwrap();
            backend.restore(before.as_ref()).unwrap();
            assert!(set(&paths, &backend, &root.join("exe"), before.is_none()).is_err());
            assert_eq!(backend.read().unwrap(), before);
            assert_eq!(
                std::fs::read(paths.config.join("config.json")).unwrap(),
                config_before
            );
        }
    }
    #[test]
    fn only_launch_field_changes_in_raw_config() {
        let (paths, backend, root) = fixture(Os::Mac);
        std::fs::create_dir_all(&paths.config).unwrap();
        let mut raw = serde_json::to_value(wallpaper_core::config::Settings::default()).unwrap();
        raw["pause"]["resumeDebounceMs"] = serde_json::json!(0);
        raw["futureField"] = serde_json::json!({"preserved":true});
        std::fs::write(
            paths.config.join("config.json"),
            serde_json::to_vec(&raw).unwrap(),
        )
        .unwrap();
        assert_eq!(set(&paths, &backend, &root.join("exe"), true).unwrap(), 0);
        raw["startup"]["launchAtLogin"] = serde_json::json!(true);
        let after: serde_json::Value =
            serde_json::from_slice(&std::fs::read(paths.config.join("config.json")).unwrap())
                .unwrap();
        assert_eq!(after, raw);
    }
    #[test]
    fn initial_window_respects_setting_only_for_run() {
        let mut s = wallpaper_core::config::Settings::default();
        assert!(should_open_settings(false, &s));
        s.startup.open_settings = false;
        assert!(!should_open_settings(false, &s));
        assert!(should_open_settings(true, &s));
    }
}
