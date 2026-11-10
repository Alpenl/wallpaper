use serde_json::{Value, json};
use wallpaper_core::{
    config::{BatteryPolicy, Lang, Settings, Theme},
    displays::{Assignments, DisplayId},
    legacy::{LegacyError, convert_assignments, convert_config, convert_library},
    library::{Fit, Kind, Library},
};

#[test]
fn config_fixture_matches_old_defaults_and_records_loss() {
    let converted = convert_config(include_str!("fixtures/legacy/config.json")).unwrap();
    let settings = converted.value;
    assert_eq!(settings.schema_version, 1);
    assert_eq!(settings.pause.battery_policy, BatteryPolicy::Pause);
    assert_eq!(settings.pause.low_battery_pct, 20);
    assert_eq!(settings.pause.resume_debounce_ms, 300);
    assert!(!settings.pause.on_fullscreen);
    assert!(settings.pause.on_covered);
    assert!(settings.startup.launch_at_login);
    assert!(settings.startup.open_settings);
    assert_eq!(settings.ui.lang, Lang::ZhCn);
    assert_eq!(settings.ui.theme, Theme::Dark);
    for name in [
        "fps15",
        "lowBatteryPct",
        "resumeDebounceMs",
        "lang",
        "theme",
    ] {
        assert!(
            converted.notes.iter().any(|note| note.contains(name)),
            "{name}"
        );
    }
}

#[test]
fn config_valid_enums_ranges_and_missing_fields() {
    assert_eq!(convert_config("{}").unwrap().value, Settings::default());
    for theme in ["system", "light", "dark"] {
        let value = json!({"pause":{"batteryPolicy":"ignore", "lowBatteryPct":100,"resumeDebounceMs":10000},"ui":{"lang":"en","theme":theme}});
        let result = convert_config(&value.to_string()).unwrap();
        assert!(result.notes.is_empty());
        assert_eq!(result.value.pause.battery_policy, BatteryPolicy::Ignore);
        assert_eq!(result.value.pause.low_battery_pct, 100);
        assert_eq!(result.value.pause.resume_debounce_ms, 10000);
        assert_eq!(result.value.ui.lang, Lang::En);
    }
    for bad in [
        json!(-1),
        json!(10001),
        json!(1.5),
        json!("bad"),
        Value::Null,
    ] {
        let value =
            json!({"pause":{"batteryPolicy":"unknown","lowBatteryPct":bad,"resumeDebounceMs":bad}});
        let result = convert_config(&value.to_string()).unwrap();
        assert_eq!(result.value, Settings::default());
        assert_eq!(result.notes.len(), 3);
    }
    let result = convert_config(r#"{"pause":{"resumeDebounceMs":10001}}"#).unwrap();
    assert_eq!(result.value, Settings::default());
    assert_eq!(result.notes.len(), 1);
}

#[test]
fn library_fixture_preserves_supported_items_and_reports_skips() {
    let result = convert_library(include_str!("fixtures/legacy/library.json")).unwrap();
    assert_eq!(result.value.schema_version, 1);
    assert_eq!(result.value.items.len(), 5);
    let video = result.value.find("sample-video").unwrap();
    assert_eq!(video.kind, Kind::Video);
    assert_eq!(video.title, "Synthetic video");
    assert_eq!(video.entry, "clip.mp4");
    assert_eq!(
        (
            video.width,
            video.height,
            video.duration_sec,
            video.size_bytes
        ),
        (Some(1920), Some(1080), Some(2.5), Some(4096))
    );
    assert_eq!(video.added_at, "2020-01-01T00:00:00Z");
    assert_eq!(video.fit, Fit::Fit);
    assert_eq!(result.value.find("sample-image").unwrap().fit, Fit::Fill);
    assert_eq!(result.value.find("sample-null").unwrap().fit, Fit::Fill);
    assert_eq!(
        result.value.find("sample-stretch").unwrap().fit,
        Fit::Stretch
    );
    let invalid = result.value.find("sample-invalid").unwrap();
    assert_eq!(
        (
            invalid.width,
            invalid.height,
            invalid.duration_sec,
            invalid.size_bytes
        ),
        (None, None, None, None)
    );
    assert_eq!(invalid.fit, Fit::Fill);
    for id in ["sample-web", "sample-unknown", "sample-no-type"] {
        assert!(result.value.find(id).is_none());
        assert!(
            result
                .notes
                .iter()
                .any(|note| note.contains(id) && note.contains("unsupported type"))
        );
    }
    for field in ["width", "height", "durationSec", "sizeBytes", "fit"] {
        assert!(
            result
                .notes
                .iter()
                .any(|note| note.contains("sample-invalid") && note.contains(field))
        );
    }
    assert_eq!(convert_library("{}").unwrap().value, Library::default());
}

#[test]
fn assignments_fixture_retains_independent_and_options_only_displays() {
    let result = convert_assignments(include_str!("fixtures/legacy/assignments.json")).unwrap();
    assert_eq!(result.value.default.as_deref(), Some("sample-video"));
    assert_eq!(result.value.displays.len(), 4);
    let a = &result.value.displays[&DisplayId("display-a".into())];
    assert!(a.paused);
    assert_eq!(a.item.as_deref(), Some("sample-video"));
    assert_eq!(
        result.value.aliases[&DisplayId("display-a".into())],
        "Synthetic display A"
    );
    let only = &result.value.displays[&DisplayId("display-only-options".into())];
    assert!(only.paused);
    assert!(only.item.is_none());
    assert!(
        result.value.displays[&DisplayId("display-empty".into())]
            .item
            .is_none()
    );
    assert!(!result.value.displays[&DisplayId("display-b".into())].paused);
    assert!(result.notes.iter().any(|note| note.contains("span")));
    for field in ["fit", "speed", "audio"] {
        for id in ["display-a", "display-only-options"] {
            assert!(
                result
                    .notes
                    .iter()
                    .any(|note| note.contains(id) && note.contains(field))
            );
        }
    }
    assert!(!result.notes.iter().any(|note| note.contains("display-b")));
    assert_eq!(
        convert_assignments("{}").unwrap().value,
        Assignments::default()
    );
    let serialized = serde_json::to_string(&result.value).unwrap();
    assert!(serialized.find("display-a").unwrap() < serialized.find("display-b").unwrap());
}

#[test]
fn legacy_only_rejects_invalid_json_and_future_versions() {
    for version in 0..=2 {
        assert!(convert_config(&json!({"schemaVersion":version}).to_string()).is_ok());
    }
    assert!(matches!(
        convert_config(r#"{"schemaVersion":3}"#),
        Err(LegacyError::NewerVersion { .. })
    ));
    assert!(matches!(
        convert_library(r#"{"schemaVersion":5}"#),
        Err(LegacyError::NewerVersion { .. })
    ));
    assert!(matches!(
        convert_assignments(r#"{"schemaVersion":4}"#),
        Err(LegacyError::NewerVersion { .. })
    ));
    assert!(matches!(convert_config("{"), Err(LegacyError::Json(_))));
    assert!(matches!(convert_library("{"), Err(LegacyError::Json(_))));
    assert!(matches!(
        convert_assignments("{"),
        Err(LegacyError::Json(_))
    ));
    for malformed in [
        "null",
        "[]",
        r#"{"schemaVersion":"bad","items":false,"options":5,"pause":[]}"#,
    ] {
        assert!(convert_config(malformed).is_ok());
        assert!(convert_library(malformed).is_ok());
        assert!(convert_assignments(malformed).is_ok());
    }
}

#[test]
fn new_models_roundtrip_strict_enums_and_normalize() {
    let settings = Settings::default();
    let mut raw = serde_json::to_value(&settings).unwrap();
    assert_eq!(raw["schemaVersion"], 1);
    assert_eq!(raw["pause"]["batteryPolicy"], "pause");
    assert_eq!(raw["startup"]["launchAtLogin"], false);
    assert_eq!(
        serde_json::from_value::<Settings>(raw.clone()).unwrap(),
        settings
    );
    for (group, field) in [("pause", "batteryPolicy"), ("ui", "lang"), ("ui", "theme")] {
        let mut invalid = raw.clone();
        invalid[group][field] = json!("unknown");
        assert!(serde_json::from_value::<Settings>(invalid).is_err());
    }
    raw["pause"]["lowBatteryPct"] = json!(101);
    for debounce in [0, 10001] {
        raw["pause"]["resumeDebounceMs"] = json!(debounce);
        let mut normalized: Settings = serde_json::from_value(raw.clone()).unwrap();
        normalized.normalize();
        assert_eq!(normalized, settings);
    }
    let library = convert_library(include_str!("fixtures/legacy/library.json"))
        .unwrap()
        .value;
    let mut raw = serde_json::to_value(&library).unwrap();
    assert_eq!(
        serde_json::from_value::<Library>(raw.clone()).unwrap(),
        library
    );
    raw["items"][0]["kind"] = json!("web");
    assert!(serde_json::from_value::<Library>(raw.clone()).is_err());
    raw["items"][0]["kind"] = json!("video");
    raw["items"][0]["fit"] = json!("unknown");
    assert!(serde_json::from_value::<Library>(raw).is_err());
    let assignments = convert_assignments(include_str!("fixtures/legacy/assignments.json"))
        .unwrap()
        .value;
    assert_eq!(
        serde_json::from_str::<Assignments>(&serde_json::to_string(&assignments).unwrap()).unwrap(),
        assignments
    );
}
