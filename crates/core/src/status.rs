use crate::displays::{DisplayId, Frame};
use serde::{Deserialize, Serialize};
use std::time::{SystemTime, UNIX_EPOCH};

#[derive(Debug, Default, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Truth {
    #[default]
    Unknown,
    Yes,
    No,
}
impl From<bool> for Truth {
    fn from(value: bool) -> Self {
        if value { Self::Yes } else { Self::No }
    }
}
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Phase {
    Starting,
    Confirmed,
    Failed,
    Stopped,
}

impl Phase {
    pub fn as_str(&self) -> &'static str {
        match self {
            Self::Starting => "starting",
            Self::Confirmed => "confirmed",
            Self::Failed => "failed",
            Self::Stopped => "stopped",
        }
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Request {
    pub path: String,
    pub backend: String,
}
#[derive(Debug, Default, Clone, Serialize, Deserialize)]
pub struct Evidence {
    pub visible: Truth,
    pub on_screen: Truth,
    pub occluded: Truth,
    pub attached: Truth,
    pub window: usize,
    pub parent: usize,
    pub ready_for_display: Truth,
    pub position: Option<f64>,
    pub presented_frames: Option<u64>,
    pub loops: Option<u64>,
    pub child_pid: u32,
}
impl Evidence {
    pub fn displayed(&self) -> bool {
        self.visible == Truth::Yes
            && self.on_screen == Truth::Yes
            && self.attached == Truth::Yes
            && self.ready_for_display == Truth::Yes
    }
}
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct State {
    pub session: String,
    pub pid: u32,
    pub phase: Phase,
    pub reason: String,
    pub backend: String,
    pub generation: u64,
    pub pending: Vec<u64>,
    pub desired: Option<Request>,
    pub confirmed_generation: u64,
    #[serde(flatten)]
    pub evidence: Evidence,
    pub progress: Truth,
    pub timestamp: String,
}

// RFC3339 UTC, matching Go time.Time's JSON meaning without a platform/date crate.
pub fn timestamp() -> String {
    let d = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default();
    let seconds = d.as_secs();
    let z = (seconds / 86400) as i64 + 719468;
    let era = z / 146097;
    let doe = z - era * 146097;
    let yoe = (doe - doe / 1460 + doe / 36524 - doe / 146096) / 365;
    let mut year = yoe + era * 400;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let day = doy - (153 * mp + 2) / 5 + 1;
    let month = mp + if mp < 10 { 3 } else { -9 };
    year += i64::from(month <= 2);
    format!(
        "{year:04}-{month:02}-{day:02}T{:02}:{:02}:{:02}.{:03}Z",
        seconds / 3600 % 24,
        seconds / 60 % 60,
        seconds % 60,
        d.subsec_millis()
    )
}

pub const STATUS_SCHEMA: u32 = 3;

#[derive(Serialize)]
pub struct DisplayStatus<'a> {
    pub display: &'a DisplayId,
    pub name: &'a str,
    pub primary: bool,
    pub frame: Frame,
    pub scale: f64,
    pub paused: bool,
    #[serde(rename = "pausedReasons")]
    pub paused_reasons: &'a [crate::policy::Reason],
    #[serde(rename = "resumePending")]
    pub resume_pending: bool,
    #[serde(flatten)]
    pub state: &'a State,
}

#[derive(Serialize)]
pub struct StatusFile<'a> {
    pub schema_version: u32,
    pub pid: u32,
    pub timestamp: String,
    pub error: &'a str,
    #[serde(rename = "syntheticSignals", skip_serializing_if = "is_false")]
    pub synthetic_signals: bool,
    pub displays: Vec<DisplayStatus<'a>>,
}

fn is_false(value: &bool) -> bool {
    !value
}

pub fn render_status(mut status: StatusFile<'_>) -> Result<Vec<u8>, serde_json::Error> {
    let mut bytes = Vec::new();
    status.displays.sort_by(|a, b| a.display.cmp(b.display));
    serde_json::to_writer_pretty(&mut bytes, &status)?;
    bytes.push(b'\n');
    Ok(bytes)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::Session;
    use std::time::Duration;

    fn session() -> Session {
        Session::new("primary".into(), "clip.mp4".into(), "avf", Duration::ZERO)
    }
    #[test]
    fn json_contract() {
        let s = session();
        let json = serde_json::to_value(s.state).unwrap();
        let mut actual: Vec<_> = json
            .as_object()
            .unwrap()
            .keys()
            .map(String::as_str)
            .collect();
        actual.sort_unstable();
        let mut expected = vec![
            "session",
            "pid",
            "phase",
            "reason",
            "backend",
            "generation",
            "pending",
            "desired",
            "confirmed_generation",
            "visible",
            "on_screen",
            "occluded",
            "attached",
            "window",
            "parent",
            "ready_for_display",
            "position",
            "presented_frames",
            "loops",
            "child_pid",
            "progress",
            "timestamp",
        ];
        expected.sort_unstable();
        assert_eq!(actual, expected);
        assert_eq!(json["visible"], "unknown");
        assert_eq!(json["phase"], "starting");
        assert!(json["position"].is_null());
        assert_eq!(serde_json::to_string(&Truth::Yes).unwrap(), "\"yes\"");
        assert_eq!(serde_json::to_string(&Truth::No).unwrap(), "\"no\"");
    }
    #[test]
    fn status_v3_json_contract_sorted_and_newline() {
        let session = session();
        let a = DisplayId("a".into());
        let z = DisplayId("z".into());
        let display = |id| DisplayStatus {
            display: id,
            name: "Monitor",
            primary: id == &a,
            frame: Frame {
                x: -100,
                y: 20,
                width: 100,
                height: 200,
            },
            scale: 2.0,
            paused: true,
            paused_reasons: &[],
            resume_pending: true,
            state: &session.state,
        };
        let bytes = render_status(StatusFile {
            schema_version: STATUS_SCHEMA,
            pid: 123,
            timestamp: "2026-11-09T00:00:00Z".into(),
            error: "",
            synthetic_signals: false,
            displays: vec![display(&z), display(&a)],
        })
        .unwrap();
        assert_eq!(bytes.last(), Some(&b'\n'));
        assert!(bytes.windows(3).any(|w| w == b"\n  "));
        let json: serde_json::Value = serde_json::from_slice(&bytes).unwrap();
        let mut keys: Vec<_> = json
            .as_object()
            .unwrap()
            .keys()
            .map(String::as_str)
            .collect();
        keys.sort_unstable();
        assert_eq!(
            keys,
            ["displays", "error", "pid", "schema_version", "timestamp"]
        );
        assert_eq!(json["schema_version"], 3);
        assert_eq!(json["pid"], 123);
        assert_eq!(json["displays"][0]["display"], "a");
        assert_eq!(json["displays"][1]["display"], "z");
        let mut expected = serde_json::to_value(&session.state)
            .unwrap()
            .as_object()
            .unwrap()
            .clone();
        expected.extend(serde_json::json!({"display": "a", "name": "Monitor", "primary": true, "frame": {"x": -100, "y": 20, "width": 100, "height": 200}, "scale": 2.0, "paused": true, "pausedReasons": [], "resumePending": true}).as_object().unwrap().clone());
        assert_eq!(json["displays"][0], serde_json::Value::Object(expected));
    }
    #[test]
    fn synthetic_flag_and_reason_names() {
        use crate::policy::Reason::*;
        let bytes = render_status(StatusFile {
            schema_version: STATUS_SCHEMA,
            pid: 1,
            timestamp: timestamp(),
            error: "",
            synthetic_signals: true,
            displays: vec![],
        })
        .unwrap();
        let json: serde_json::Value = serde_json::from_slice(&bytes).unwrap();
        assert_eq!(json["syntheticSignals"], true);
        assert_eq!(
            serde_json::to_value([
                User,
                UserDisplay,
                Locked,
                Asleep,
                Battery,
                LowBattery,
                Fullscreen,
                Covered
            ])
            .unwrap(),
            serde_json::json!([
                "user",
                "userDisplay",
                "locked",
                "asleep",
                "battery",
                "lowBattery",
                "fullscreen",
                "covered"
            ])
        );
    }
    #[test]
    fn phase_as_str_matches_serialization() {
        for phase in [
            Phase::Starting,
            Phase::Confirmed,
            Phase::Failed,
            Phase::Stopped,
        ] {
            assert_eq!(
                phase.as_str(),
                serde_json::to_value(phase).unwrap().as_str().unwrap()
            );
        }
    }
}
