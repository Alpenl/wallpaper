use crate::{Phase, State};

pub struct Snapshot {
    pub phase: Phase,
    pub reason: String,
    pub position: Option<f64>,
    pub loops: Option<u64>,
    pub video: String,
    pub paused: bool,
}

impl Snapshot {
    pub fn idle() -> Self {
        Self {
            phase: Phase::Stopped,
            reason: "未分配壁纸".into(),
            position: None,
            loops: None,
            video: String::new(),
            paused: false,
        }
    }

    pub fn new(state: &State, paused: bool) -> Self {
        Self {
            phase: state.phase,
            reason: state.reason.clone(),
            position: state.evidence.position,
            loops: state.evidence.loops,
            video: state
                .desired
                .as_ref()
                .map(|r| r.path.clone())
                .unwrap_or_default(),
            paused,
        }
    }
}
