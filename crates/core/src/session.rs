use crate::{Evidence, Phase, Request, State, Truth, timestamp};
use std::time::Duration;

pub fn progress(a: &Evidence, b: &Evidence) -> Truth {
    if let (Some(a), Some(b)) = (a.presented_frames, b.presented_frames)
        && b > a
    {
        return Truth::Yes;
    }
    match (a.position, b.position) {
        (Some(a), Some(b)) if a.is_finite() && b.is_finite() => (a != b).into(),
        _ => Truth::Unknown,
    }
}

pub struct Session {
    pub state: State,
    previous: Evidence,
    deadline: Duration,
    misses: u8,
    paused: bool,
    held: bool,
    still: bool,
    resume_until: Option<Duration>,
}
impl Session {
    pub fn new(name: String, path: String, backend: &str, now: Duration) -> Self {
        Self {
            state: State {
                session: name,
                pid: std::process::id(),
                phase: Phase::Starting,
                reason: String::new(),
                backend: backend.into(),
                generation: 1,
                pending: vec![1],
                desired: Some(Request {
                    path,
                    backend: backend.into(),
                }),
                confirmed_generation: 0,
                evidence: Evidence::default(),
                progress: Truth::Unknown,
                timestamp: timestamp(),
            },
            previous: Evidence::default(),
            deadline: now + Duration::from_secs(15),
            misses: 0,
            paused: false,
            held: false,
            still: false,
            resume_until: None,
        }
    }
    pub fn set_still(&mut self) {
        self.still = true;
        self.state.progress = Truth::Unknown;
    }
    pub fn loaded(&mut self) {
        self.state.pending.clear();
    }
    pub fn restart(&mut self, now: Duration) {
        self.state.generation += 1;
        self.state.phase = Phase::Starting;
        self.state.reason.clear();
        self.state.confirmed_generation = 0;
        self.state.evidence = Evidence::default();
        self.state.progress = Truth::Unknown;
        self.previous = Evidence::default();
        self.deadline = now + Duration::from_secs(15);
        self.misses = 0;
        self.resume_until = None;
    }
    pub fn set_paused(&mut self, paused: bool, now: Duration) {
        if self.paused == paused {
            return;
        }
        self.paused = paused;
        self.misses = 0;
        if !paused {
            self.resume(now);
        }
    }
    pub fn set_held(&mut self, held: bool, now: Duration) {
        if self.held == held {
            return;
        }
        self.held = held;
        if !held {
            self.misses = 0;
            self.resume(now);
        }
    }
    fn resume(&mut self, now: Duration) {
        self.misses = 0;
        self.previous = Evidence::default();
        self.deadline = now + Duration::from_secs(15);
        if self.state.phase == Phase::Confirmed {
            self.resume_until = Some(self.deadline);
        }
    }
    pub fn sample(&mut self, evidence: Evidence, now: Duration) {
        if matches!(self.state.phase, Phase::Failed | Phase::Stopped) {
            return;
        }
        if self.held {
            self.state.progress = if self.still {
                Truth::Unknown
            } else {
                progress(&self.previous, &evidence)
            };
            self.state.evidence = evidence;
            self.stamp();
            return;
        }
        if self.state.phase == Phase::Starting
            && (self.still || !self.paused)
            && now >= self.deadline
        {
            self.fail("evidence timeout: display and playback progress not confirmed");
            return;
        }
        self.state.progress = if self.still {
            Truth::Unknown
        } else {
            progress(&self.previous, &evidence)
        };
        let displayed = evidence.displayed();
        if displayed && (evidence.position.is_some() || evidence.presented_frames.is_some()) {
            self.previous = evidence.clone();
        } else if self.state.phase != Phase::Confirmed {
            self.previous = Evidence::default();
        }
        self.state.evidence = evidence;
        // For videos, an intentional pause only preserves an already-confirmed
        // display. Stills depend solely on display evidence.
        if displayed
            && (self.still
                || self.state.progress == Truth::Yes
                || (self.paused && self.state.phase == Phase::Confirmed))
        {
            self.misses = 0;
            self.state.phase = Phase::Confirmed;
            self.state.confirmed_generation = self.state.generation;
        } else if self.state.phase == Phase::Confirmed {
            if self.paused || self.resume_until.is_none_or(|until| now >= until) {
                self.misses += 1;
            }
            if self.misses >= 3 {
                self.fail("evidence lost: display or playback progress missing for three samples");
            }
        } else if (self.still || !self.paused) && now >= self.deadline {
            self.fail("evidence timeout: display and playback progress not confirmed");
        }
        self.stamp();
    }
    pub fn fail(&mut self, reason: impl Into<String>) {
        self.state.phase = Phase::Failed;
        self.state.reason = reason.into();
        self.state.confirmed_generation = 0;
        self.stamp();
    }
    pub fn cleanup(&mut self, error: Option<String>) {
        self.state.pending.clear();
        if let Some(error) = error {
            if !self.state.reason.is_empty() {
                self.state.reason.push_str("; cleanup: ");
            }
            self.state.reason.push_str(&error);
            self.state.evidence.visible = Truth::Unknown;
            self.state.evidence.attached = Truth::Unknown;
        } else {
            self.state.evidence.visible = Truth::No;
            self.state.evidence.attached = Truth::No;
        }
    }
    pub fn stop(&mut self) {
        self.state.generation += 1;
        self.state.desired = None;
        self.state.confirmed_generation = 0;
        self.state.pending.clear();
        if self.state.phase != Phase::Failed {
            self.state.phase = Phase::Stopped;
        }
        self.stamp();
    }
    pub fn stamp(&mut self) {
        self.state.timestamp = timestamp();
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    fn ready(p: f64) -> Evidence {
        Evidence {
            visible: Truth::Yes,
            on_screen: Truth::Yes,
            attached: Truth::Yes,
            ready_for_display: Truth::Yes,
            position: Some(p),
            ..Evidence::default()
        }
    }
    fn session() -> Session {
        Session::new("primary".into(), "clip.mp4".into(), "avf", Duration::ZERO)
    }
    #[test]
    fn confirmation_requires_each_display_fact_and_progress() {
        for missing in 0..4 {
            let mut s = session();
            s.sample(ready(0.), Duration::ZERO);
            let mut e = ready(1.);
            match missing {
                0 => e.visible = Truth::Unknown,
                1 => e.on_screen = Truth::No,
                2 => e.attached = Truth::Unknown,
                _ => e.ready_for_display = Truth::No,
            }
            s.sample(e, Duration::from_secs(2));
            assert_eq!(s.state.phase, Phase::Starting);
        }
        let mut s = session();
        s.sample(ready(0.), Duration::ZERO);
        assert_eq!(s.state.phase, Phase::Starting);
        let mut e = ready(1.);
        e.occluded = Truth::Yes;
        s.sample(e, Duration::from_secs(2));
        assert_eq!(s.state.phase, Phase::Confirmed);
    }
    #[test]
    fn wrap_is_progress() {
        assert_eq!(progress(&ready(10.), &ready(0.)), Truth::Yes);
    }
    #[test]
    fn three_consecutive_soft_misses_and_recovery() {
        let mut s = session();
        s.sample(ready(0.), Duration::ZERO);
        s.sample(ready(1.), Duration::ZERO);
        for _ in 0..2 {
            s.sample(Evidence::default(), Duration::ZERO);
            assert_eq!(s.state.phase, Phase::Confirmed);
        }
        s.sample(ready(2.), Duration::ZERO);
        for _ in 0..2 {
            s.sample(ready(2.), Duration::ZERO);
            assert_eq!(s.state.phase, Phase::Confirmed);
        }
        s.sample(ready(2.), Duration::ZERO);
        assert_eq!(s.state.phase, Phase::Failed);
    }
    #[test]
    fn failed_survives_stop() {
        let mut s = session();
        s.fail("decoder failed");
        s.stop();
        assert_eq!(s.state.phase, Phase::Failed);
        assert_eq!(s.state.reason, "decoder failed");
        s.sample(ready(2.), Duration::ZERO);
        assert_eq!(s.state.phase, Phase::Failed);
    }
    #[test]
    fn timeout_restart_and_pause() {
        let mut s = session();
        s.sample(ready(0.), Duration::from_secs(15));
        assert_eq!(s.state.phase, Phase::Failed);
        s.restart(Duration::from_secs(20));
        s.sample(ready(0.), Duration::from_secs(21));
        s.sample(ready(1.), Duration::from_secs(23));
        assert_eq!(s.state.confirmed_generation, 2);
        s.set_paused(true, Duration::from_secs(24));
        for _ in 0..5 {
            s.sample(ready(1.), Duration::from_secs(30));
        }
        assert_eq!(s.state.phase, Phase::Confirmed);
        assert_eq!(s.state.progress, Truth::No);
    }
    #[test]
    fn held_confirmed_records_missing_evidence_without_failing() {
        let mut s = session();
        s.sample(ready(0.), Duration::ZERO);
        s.sample(ready(1.), Duration::from_secs(2));
        // Existing soft misses must not grow while held.
        s.sample(Evidence::default(), Duration::from_secs(4));
        s.sample(Evidence::default(), Duration::from_secs(6));
        s.set_held(true, Duration::from_secs(7));
        for seconds in 8..3600 {
            s.set_held(true, Duration::from_secs(seconds));
            let evidence = Evidence {
                on_screen: Truth::No,
                position: Some(seconds as f64),
                ..Evidence::default()
            };
            s.sample(evidence, Duration::from_secs(seconds));
            assert_eq!(s.state.phase, Phase::Confirmed);
            assert_eq!(s.state.evidence.on_screen, Truth::No);
            assert_eq!(s.state.evidence.position, Some(seconds as f64));
            assert_eq!(s.state.confirmed_generation, 1);
        }
    }
    #[test]
    fn held_starting_neither_confirms_nor_times_out() {
        let mut s = session();
        s.sample(ready(0.), Duration::ZERO);
        s.set_held(true, Duration::from_secs(1));
        for seconds in 2..100 {
            s.sample(ready(seconds as f64), Duration::from_secs(seconds));
            assert_eq!(s.state.phase, Phase::Starting);
            assert_eq!(s.state.confirmed_generation, 0);
        }
        s.set_held(false, Duration::from_secs(100));
        s.sample(ready(100.), Duration::from_secs(102));
        assert_eq!(s.state.progress, Truth::Unknown);
        assert_eq!(s.state.phase, Phase::Starting);
        s.sample(ready(101.), Duration::from_secs(104));
        assert_eq!(s.state.phase, Phase::Confirmed);
    }
    #[test]
    fn unheld_confirmed_can_recover_on_third_sample() {
        let mut s = session();
        s.sample(ready(0.), Duration::ZERO);
        s.sample(ready(1.), Duration::from_secs(2));
        s.sample(Evidence::default(), Duration::from_secs(4));
        s.sample(Evidence::default(), Duration::from_secs(6));
        s.set_held(true, Duration::from_secs(7));
        s.sample(Evidence::default(), Duration::from_secs(100));
        s.set_held(false, Duration::from_secs(101));
        s.sample(Evidence::default(), Duration::from_secs(103));
        s.sample(ready(1.), Duration::from_secs(105));
        assert_eq!(s.state.progress, Truth::Unknown);
        assert_eq!(s.state.phase, Phase::Confirmed);
        s.sample(ready(1.1), Duration::from_secs(107));
        assert_eq!(s.state.progress, Truth::Yes);
        assert_eq!(s.state.phase, Phase::Confirmed);
    }
    #[test]
    fn unheld_missing_evidence_still_fails_and_startup_deadline_resets_once() {
        let mut s = session();
        s.sample(ready(0.), Duration::ZERO);
        s.sample(ready(1.), Duration::from_secs(2));
        s.set_held(true, Duration::from_secs(3));
        s.set_held(false, Duration::from_secs(100));
        for seconds in [102, 104, 114, 115, 116] {
            s.set_held(false, Duration::from_secs(seconds));
            s.sample(Evidence::default(), Duration::from_secs(seconds));
            assert_eq!(s.state.phase, Phase::Confirmed);
        }
        s.set_held(false, Duration::from_secs(117));
        s.sample(Evidence::default(), Duration::from_secs(117));
        assert_eq!(s.state.phase, Phase::Failed);

        let mut s = session();
        s.set_held(true, Duration::ZERO);
        s.sample(Evidence::default(), Duration::from_secs(100));
        s.set_held(false, Duration::from_secs(100));
        s.sample(Evidence::default(), Duration::from_secs(114));
        assert_eq!(s.state.phase, Phase::Starting);
        s.set_held(false, Duration::from_secs(115));
        s.sample(Evidence::default(), Duration::from_secs(115));
        assert_eq!(s.state.phase, Phase::Failed);
    }
    #[test]
    fn paused_idempotence_keeps_baseline_and_resume_grace_is_bounded() {
        let mut s = session();
        s.sample(ready(0.), Duration::ZERO);
        s.set_paused(false, Duration::from_secs(1));
        s.sample(ready(1.), Duration::from_secs(2));
        assert_eq!(s.state.phase, Phase::Confirmed);
        s.set_paused(true, Duration::from_secs(3));
        s.set_paused(false, Duration::from_secs(4));
        for second in 4..19 {
            s.set_paused(false, Duration::from_secs(second));
            s.sample(Evidence::default(), Duration::from_secs(second));
            assert_eq!(s.misses, 0);
        }
        for second in 19..21 {
            s.sample(Evidence::default(), Duration::from_secs(second));
            assert_eq!(s.state.phase, Phase::Confirmed);
        }
        s.sample(Evidence::default(), Duration::from_secs(21));
        assert_eq!(s.state.phase, Phase::Failed);
    }
    #[test]
    fn paused_video_still_requires_display_and_hard_errors_ignore_grace() {
        let mut s = session();
        s.sample(ready(0.), Duration::ZERO);
        s.sample(ready(1.), Duration::ZERO);
        s.set_paused(true, Duration::ZERO);
        for second in 1..=3 {
            s.set_paused(true, Duration::from_secs(second));
            s.sample(Evidence::default(), Duration::from_secs(second));
        }
        assert_eq!(s.state.phase, Phase::Failed);
        let mut s = session();
        s.sample(ready(0.), Duration::ZERO);
        s.sample(ready(1.), Duration::ZERO);
        s.set_paused(true, Duration::ZERO);
        s.set_paused(false, Duration::ZERO);
        s.fail("hard poll error");
        assert_eq!(s.state.phase, Phase::Failed);
    }
    fn still() -> Session {
        let mut s = session();
        s.set_still();
        s
    }
    fn still_ready() -> Evidence {
        let mut e = ready(0.);
        e.position = None;
        e
    }
    #[test]
    fn still_first_display_confirms_without_progress() {
        let mut s = still();
        s.sample(still_ready(), Duration::ZERO);
        assert_eq!(s.state.phase, Phase::Confirmed);
        assert_eq!(s.state.confirmed_generation, 1);
        assert_eq!(s.state.progress, Truth::Unknown);
        assert_eq!(s.state.evidence.position, None);
        assert_eq!(s.state.evidence.presented_frames, None);
    }
    #[test]
    fn still_three_losses_fail_and_recovery_clears_misses() {
        for missing in 0..4 {
            let mut s = still();
            s.sample(still_ready(), Duration::ZERO);
            let mut lost = still_ready();
            match missing {
                0 => lost.visible = Truth::No,
                1 => lost.on_screen = Truth::Unknown,
                2 => lost.attached = Truth::No,
                _ => lost.ready_for_display = Truth::No,
            }
            for _ in 0..2 {
                s.sample(lost.clone(), Duration::ZERO);
                assert_eq!(s.state.phase, Phase::Confirmed);
            }
            s.sample(still_ready(), Duration::ZERO);
            for _ in 0..2 {
                s.sample(lost.clone(), Duration::ZERO);
                assert_eq!(s.state.phase, Phase::Confirmed);
            }
            s.sample(lost, Duration::ZERO);
            assert_eq!(s.state.phase, Phase::Failed);
            assert_eq!(s.state.progress, Truth::Unknown);
        }
    }
    #[test]
    fn still_restart_requires_fresh_display() {
        let mut s = still();
        s.sample(still_ready(), Duration::ZERO);
        s.restart(Duration::from_secs(1));
        assert_eq!(s.state.confirmed_generation, 0);
        s.sample(Evidence::default(), Duration::from_secs(2));
        assert_eq!(s.state.phase, Phase::Starting);
        s.sample(still_ready(), Duration::from_secs(3));
        assert_eq!(s.state.phase, Phase::Confirmed);
        assert_eq!(s.state.confirmed_generation, 2);
        assert_eq!(s.state.progress, Truth::Unknown);
    }
    #[test]
    fn still_pause_does_not_change_confirmation_loss_or_timeout() {
        for paused in [false, true] {
            let mut s = still();
            s.set_paused(paused, Duration::ZERO);
            s.sample(still_ready(), Duration::ZERO);
            assert_eq!(s.state.phase, Phase::Confirmed);
            for seconds in 1..=3 {
                s.sample(Evidence::default(), Duration::from_secs(seconds));
            }
            assert_eq!(s.state.phase, Phase::Failed);
            let mut s = still();
            s.set_paused(paused, Duration::ZERO);
            s.sample(Evidence::default(), Duration::from_secs(15));
            assert_eq!(s.state.phase, Phase::Failed);
        }
    }
    #[test]
    fn still_held_does_not_confirm_timeout_or_accumulate_misses() {
        let mut s = still();
        s.set_held(true, Duration::ZERO);
        s.sample(still_ready(), Duration::from_secs(100));
        assert_eq!(s.state.phase, Phase::Starting);
        s.sample(Evidence::default(), Duration::from_secs(101));
        assert_eq!(s.state.phase, Phase::Starting);
        s.set_held(false, Duration::from_secs(102));
        s.sample(still_ready(), Duration::from_secs(103));
        assert_eq!(s.state.phase, Phase::Confirmed);
        s.set_held(true, Duration::from_secs(104));
        for second in 105..110 {
            s.sample(Evidence::default(), Duration::from_secs(second));
            assert_eq!(s.state.phase, Phase::Confirmed);
        }
        assert_eq!(s.state.progress, Truth::Unknown);
    }
    #[test]
    fn still_progress_remains_unknown_even_with_backend_counters() {
        for held in [false, true] {
            let mut s = still();
            s.set_held(held, Duration::ZERO);
            for frame in 0..5 {
                let mut e = still_ready();
                e.presented_frames = Some(frame);
                s.sample(e, Duration::from_secs(frame));
                assert_eq!(s.state.progress, Truth::Unknown);
            }
        }
    }
    #[test]
    fn still_resume_grace_is_bounded_and_idempotent() {
        let mut s = still();
        s.sample(still_ready(), Duration::ZERO);
        s.set_paused(true, Duration::ZERO);
        s.set_paused(false, Duration::from_secs(1));
        for second in 1..16 {
            s.set_paused(false, Duration::from_secs(second));
            s.sample(Evidence::default(), Duration::from_secs(second));
            assert_eq!(s.state.phase, Phase::Confirmed);
        }
        for second in 16..=18 {
            s.sample(Evidence::default(), Duration::from_secs(second));
        }
        assert_eq!(s.state.phase, Phase::Failed);
    }
}
