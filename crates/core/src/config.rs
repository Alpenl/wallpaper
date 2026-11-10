use serde::{Deserialize, Serialize};

pub const SCHEMA_VERSION: u32 = 1;

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Settings {
    pub schema_version: u32,
    pub pause: PauseSettings,
    pub startup: StartupSettings,
    pub ui: UiSettings,
}

impl Default for Settings {
    fn default() -> Self {
        Self {
            schema_version: SCHEMA_VERSION,
            pause: PauseSettings::default(),
            startup: StartupSettings::default(),
            ui: UiSettings::default(),
        }
    }
}

impl Settings {
    pub fn normalize(&mut self) {
        let defaults = PauseSettings::default();
        if self.pause.low_battery_pct > 100 {
            self.pause.low_battery_pct = defaults.low_battery_pct;
        }
        if self.pause.resume_debounce_ms == 0 || self.pause.resume_debounce_ms > 10_000 {
            self.pause.resume_debounce_ms = defaults.resume_debounce_ms;
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct PauseSettings {
    pub battery_policy: BatteryPolicy,
    pub low_battery_pct: u8,
    pub resume_debounce_ms: u32,
    pub on_fullscreen: bool,
    pub on_covered: bool,
}

impl Default for PauseSettings {
    fn default() -> Self {
        Self {
            battery_policy: BatteryPolicy::Pause,
            low_battery_pct: 20,
            resume_debounce_ms: 300,
            on_fullscreen: true,
            on_covered: true,
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum BatteryPolicy {
    Pause,
    Ignore,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct StartupSettings {
    pub launch_at_login: bool,
    pub open_settings: bool,
}

impl Default for StartupSettings {
    fn default() -> Self {
        Self {
            launch_at_login: false,
            open_settings: true,
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct UiSettings {
    pub lang: Lang,
    pub theme: Theme,
}

impl Default for UiSettings {
    fn default() -> Self {
        Self {
            lang: Lang::ZhCn,
            theme: Theme::Dark,
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
pub enum Lang {
    #[serde(rename = "zh-CN")]
    ZhCn,
    #[serde(rename = "en")]
    En,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Theme {
    System,
    Light,
    Dark,
}
