//! Pure, deliberately tolerant conversion of the Go file formats.
use crate::{
    config::{BatteryPolicy, Settings},
    displays::{Assignment, Assignments, DisplayId},
    library::{Fit, Item, Kind, Library},
};
use serde::de::DeserializeOwned;
use serde_json::Value;
use std::fmt;

#[derive(Debug)]
pub struct Converted<T> {
    pub value: T,
    pub notes: Vec<String>,
}

#[derive(Debug)]
pub enum LegacyError {
    Json(serde_json::Error),
    NewerVersion { found: u64, supported: u64 },
}

impl fmt::Display for LegacyError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Json(error) => write!(f, "invalid legacy JSON: {error}"),
            Self::NewerVersion { found, supported } => {
                write!(f, "legacy schemaVersion {found} exceeds {supported}")
            }
        }
    }
}

impl std::error::Error for LegacyError {}

fn parse(json: &str, supported: u64) -> Result<Value, LegacyError> {
    let value: Value = serde_json::from_str(json).map_err(LegacyError::Json)?;
    if let Some(found) = value["schemaVersion"].as_u64()
        && found > supported
    {
        return Err(LegacyError::NewerVersion { found, supported });
    }
    Ok(value)
}

// Missing fields use defaults. Present but malformed fields also leave a note.
fn field<T: DeserializeOwned>(value: &Value, key: &str, default: T, notes: &mut Vec<String>) -> T {
    match value.get(key) {
        None => default,
        Some(raw) => match serde_json::from_value(raw.clone()) {
            Ok(value) => value,
            Err(_) => {
                notes.push(format!("{key}: invalid value {raw}; using default"));
                default
            }
        },
    }
}

pub fn convert_config(json: &str) -> Result<Converted<Settings>, LegacyError> {
    let raw = parse(json, 2)?;
    let mut value = Settings::default();
    let mut notes = Vec::new();
    let pause = &raw["pause"];
    if pause["batteryPolicy"] == "fps15" {
        value.pause.battery_policy = BatteryPolicy::Pause;
        notes.push("batteryPolicy: fps15 converted to pause".into());
    } else {
        value.pause.battery_policy = field(
            pause,
            "batteryPolicy",
            value.pause.battery_policy,
            &mut notes,
        );
    }
    value.pause.low_battery_pct = field(
        pause,
        "lowBatteryPct",
        value.pause.low_battery_pct,
        &mut notes,
    );
    value.pause.resume_debounce_ms = field(
        pause,
        "resumeDebounceMs",
        value.pause.resume_debounce_ms,
        &mut notes,
    );
    if value.pause.low_battery_pct > 100 {
        notes.push("lowBatteryPct: outside 0..=100; using default".into());
    }
    if value.pause.resume_debounce_ms == 0 || value.pause.resume_debounce_ms > 10_000 {
        notes.push("resumeDebounceMs: outside 1..=10000; using default".into());
    }
    value.pause.on_fullscreen = field(pause, "onFullscreen", value.pause.on_fullscreen, &mut notes);
    value.pause.on_covered = field(pause, "onCovered", value.pause.on_covered, &mut notes);
    value.startup.launch_at_login = field(
        &raw["startup"],
        "launchAtLogin",
        value.startup.launch_at_login,
        &mut notes,
    );
    value.startup.open_settings = field(
        &raw["startup"],
        "openSettings",
        value.startup.open_settings,
        &mut notes,
    );
    value.ui.lang = field(&raw["ui"], "lang", value.ui.lang, &mut notes);
    value.ui.theme = field(&raw["ui"], "theme", value.ui.theme, &mut notes);
    value.normalize();
    Ok(Converted { value, notes })
}

pub fn convert_library(json: &str) -> Result<Converted<Library>, LegacyError> {
    let raw = parse(json, 4)?;
    let mut value = Library::default();
    let mut notes = Vec::new();
    let items: Vec<Value> = field(&raw, "items", Vec::new(), &mut notes);
    for raw in items {
        let id: String = field(&raw, "id", String::new(), &mut notes);
        let kind = match raw["type"].as_str() {
            Some("video") => Kind::Video,
            Some("image") => Kind::Image,
            _ => {
                notes.push(format!(
                    "item {id}: skipped unsupported type {}",
                    raw["type"]
                ));
                continue;
            }
        };
        let mut item_notes = Vec::new();
        let fit = if raw["props"]["fit"].is_null() || raw["props"]["fit"] == "" {
            Fit::Fill
        } else {
            field(&raw["props"], "fit", Fit::Fill, &mut item_notes)
        };
        let mut duration_sec: Option<f64> = field(&raw, "durationSec", None, &mut item_notes);
        if duration_sec.is_some_and(|n| n < 0.0 || !n.is_finite()) {
            item_notes.push("durationSec: outside nonnegative finite range; using default".into());
            duration_sec = None;
        }
        let item = Item {
            id: id.clone(),
            title: field(&raw, "title", String::new(), &mut item_notes),
            kind,
            entry: field(&raw, "entry", String::new(), &mut item_notes),
            width: field(&raw, "width", None, &mut item_notes),
            height: field(&raw, "height", None, &mut item_notes),
            duration_sec,
            size_bytes: field(&raw, "sizeBytes", None, &mut item_notes),
            added_at: field(&raw, "addedAt", String::new(), &mut item_notes),
            fit,
        };
        notes.extend(
            item_notes
                .into_iter()
                .map(|note| format!("item {id}: {note}")),
        );
        value.items.push(item);
    }
    Ok(Converted { value, notes })
}

pub fn convert_assignments(json: &str) -> Result<Converted<Assignments>, LegacyError> {
    let raw = parse(json, 3)?;
    let mut value = Assignments::default();
    let mut notes = Vec::new();
    match raw["layout"].as_str() {
        Some("span") => {
            notes.push("layout: span discarded; independent assignments retained".into())
        }
        None | Some("" | "independent") => {}
        _ => notes.push("layout: invalid value; using independent assignments".into()),
    }
    let default: String = field(&raw, "default", String::new(), &mut notes);
    value.default = (!default.is_empty()).then_some(default);
    let displays: std::collections::BTreeMap<String, Value> =
        field(&raw, "displays", Default::default(), &mut notes);
    for (id, item) in displays {
        let item = match item.as_str() {
            Some("") => None,
            Some(item) => Some(item.to_owned()),
            None => {
                notes.push(format!("display {id}: invalid item; using default"));
                None
            }
        };
        value.displays.insert(
            DisplayId(id),
            Assignment {
                item,
                paused: false,
            },
        );
    }
    let aliases: std::collections::BTreeMap<String, Value> =
        field(&raw, "aliases", Default::default(), &mut notes);
    for (id, alias) in aliases {
        if let Some(alias) = alias.as_str() {
            value.aliases.insert(DisplayId(id), alias.to_owned());
        } else {
            notes.push(format!("alias {id}: invalid value; omitted"));
        }
    }
    let options: std::collections::BTreeMap<String, Value> =
        field(&raw, "options", Default::default(), &mut notes);
    for (id, option) in options {
        let mut option_notes = Vec::new();
        value
            .displays
            .entry(DisplayId(id.clone()))
            .or_default()
            .paused = field(&option, "paused", false, &mut option_notes);
        for key in ["fit", "speed", "audio"] {
            if let Some(v) = option.get(key)
                && !v.is_null()
                && v != ""
                && v.as_f64() != Some(0.0)
            {
                option_notes.push(format!("{key}: override {v} discarded"));
            }
        }
        notes.extend(
            option_notes
                .into_iter()
                .map(|note| format!("display {id}: {note}")),
        );
    }
    Ok(Converted { value, notes })
}
