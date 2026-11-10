use crate::tray::Tray;
use anyhow::{Result, bail};
use std::{
    cell::Cell,
    collections::BTreeMap,
    path::PathBuf,
    rc::Rc,
    time::{Duration, Instant, SystemTime},
};
use wallpaper_core::{
    DisplayStatus, Evidence, Phase, STATUS_SCHEMA, Session, Snapshot, StatusFile, Truth,
    config::PauseSettings,
    displays::{Assignments, DisplayId, DisplayInfo, Running, Target, TopologyGate, diff, resolve},
    library::{Fit, Kind, Library, Media},
    policy::{self, Gate, Power, Reason, Signals, Stable},
    recovery::{Hold, Retry},
};
use wallpaper_platform::{Desktop, MainThread};

fn log_reason(reason: &str) -> String {
    wallpaper_core::redact::redact(reason, &[]).unwrap_or_else(|_| "reason omitted".into())
}

pub(crate) enum Source {
    Play(String),
    Assigned {
        assignments: Assignments,
        library: Library,
        library_root: PathBuf,
    },
}
impl Source {
    fn targets(&self, displays: &[DisplayInfo]) -> BTreeMap<DisplayId, Target> {
        match self {
            Self::Play(path) => displays
                .iter()
                .find(|d| d.primary)
                .map(|d| {
                    (
                        d.id.clone(),
                        Target::Play {
                            item: String::new(),
                            path: path.clone(),
                            kind: Kind::Video,
                            fit: Fit::Fill,
                            paused: false,
                        },
                    )
                })
                .into_iter()
                .collect(),
            Self::Assigned {
                assignments,
                library,
                library_root,
            } => resolve(assignments, library, library_root, displays),
        }
    }
}
struct Screen {
    info: DisplayInfo,
    target: Target,
    session: Session,
    desktop: Option<Desktop>,
    next_sample: Instant,
    user_paused: bool,
    gate: Gate,
    reasons: Vec<Reason>,
    fullscreen: Stable,
    covered: Stable,
    stable_fullscreen: Truth,
    stable_covered: Truth,
    retry: Retry,
    logged: Option<(Phase, u64)>,
}
impl Screen {
    fn observe_visibility(&mut self, value: wallpaper_platform::Visibility) {
        self.stable_fullscreen = self.fullscreen.observe(value.fullscreen);
        self.stable_covered = self.covered.observe(value.covered);
    }
    fn update_info(&mut self, info: DisplayInfo) {
        if self.info.frame != info.frame {
            self.fullscreen.reset();
            self.covered.reset();
            self.stable_fullscreen = Truth::Unknown;
            self.stable_covered = Truth::Unknown;
        }
        self.info = info;
    }
    fn decide(
        &mut self,
        settings: &PauseSettings,
        global: &Signals,
        auto: bool,
        immediate: bool,
        now: Duration,
    ) -> bool {
        let signals = if auto {
            Signals {
                user_display: self.user_paused,
                fullscreen: self.stable_fullscreen,
                covered: self.stable_covered,
                ..global.clone()
            }
        } else {
            Signals {
                user_global: global.user_global,
                ..Signals::default()
            }
        };
        let reasons = policy::reasons(settings, &signals);
        let mut changed = reasons != self.reasons;
        if changed {
            log::info!("display {} pause reasons: {:?}", self.info.id.0, reasons);
        }
        let before = (self.gate.paused(), self.gate.resume_pending());
        self.gate.observe(
            !reasons.is_empty(),
            immediate && reasons.is_empty(),
            now,
            Duration::from_millis(settings.resume_debounce_ms.into()),
        );
        if before.0 != self.gate.paused() {
            log::info!(
                "display {} pause target={}",
                self.info.id.0,
                self.gate.paused()
            );
        }
        self.reasons = reasons;
        changed |= before != (self.gate.paused(), self.gate.resume_pending());
        changed
    }
    fn close(&mut self, main: MainThread) -> Option<String> {
        let error = self
            .desktop
            .as_mut()
            .and_then(|d| d.close(main).err())
            .map(|e| format!("{e:#}"));
        self.desktop.take();
        self.session.cleanup(error.clone());
        error
    }
    fn fail(&mut self, reason: String, main: MainThread, elapsed: Duration, retry_enabled: bool) {
        log::error!("display {} failed: {}", self.info.id.0, log_reason(&reason));
        self.session.fail(reason);
        self.close(main);
        self.schedule_retry(elapsed, retry_enabled);
    }
    fn schedule_retry(&mut self, elapsed: Duration, retry_enabled: bool) {
        if retry_enabled && matches!(self.target, Target::Play { .. }) {
            if self.retry.on_failure(elapsed) {
                log::info!("display {} retry scheduled", self.info.id.0);
            } else {
                log::warn!("display {} retries exhausted", self.info.id.0);
                self.session.state.reason.push_str("; retries exhausted");
            }
        }
    }
    fn sample(&mut self, main: MainThread, start: Instant, hold: bool) -> Result<bool> {
        let now = Instant::now(); // Each screen uses its own clock after preceding polls.
        let Some(desktop) = self.desktop.as_mut() else {
            self.session.set_held(hold, start.elapsed());
            return Ok(false);
        };
        #[cfg(target_os = "macos")]
        if self.session.state.phase != Phase::Starting && now < self.next_sample {
            self.session.set_held(hold, start.elapsed());
            return Ok(false);
        }
        let observed = desktop.poll(main, self.gate.paused());
        self.session.set_held(
            hold || observed
                .as_ref()
                .ok()
                .and_then(|e| e.as_ref())
                .is_some_and(Desktop::obscured),
            start.elapsed(),
        );
        let observed = observed?;
        if desktop.take_restarted() {
            self.session.restart(start.elapsed());
            self.next_sample = Instant::now() + Duration::from_secs(2);
            return Ok(true);
        }
        let sampled = if let Some(evidence) = observed {
            self.session.sample(evidence, start.elapsed());
            if self.session.state.phase == Phase::Confirmed {
                desktop.confirmed();
            }
            true
        } else if now >= self.next_sample && self.session.state.phase == Phase::Starting {
            self.session.sample(Evidence::default(), start.elapsed());
            true
        } else {
            false
        };
        if sampled {
            self.next_sample = Instant::now() + Duration::from_secs(2);
            if self.session.state.phase == Phase::Confirmed {
                self.retry.on_confirmed(start.elapsed());
            }
        }
        if self.session.state.phase == Phase::Failed {
            bail!("{}", self.session.state.reason);
        }
        Ok(sampled)
    }
}

pub(crate) struct Runtime {
    screens: BTreeMap<DisplayId, Screen>,
    gate: TopologyGate,
    topology: Vec<DisplayInfo>,
    next_topology: Instant,
    source: Source,
    pub(crate) tray: Option<Tray>,
    status: Option<PathBuf>,
    start: Instant,
    paused: bool,
    locked: bool,
    asleep: bool,
    settings: PauseSettings,
    auto: bool,
    power: Option<Power>,
    real_power: Option<Power>,
    visibility: BTreeMap<DisplayId, wallpaper_platform::Visibility>,
    real_visibility: BTreeMap<DisplayId, wallpaper_platform::Visibility>,
    next_power: Instant,
    next_visibility: Instant,
    #[cfg(debug_assertions)]
    fake_path: Option<PathBuf>,
    hold: Hold,
    last_tick: SystemTime,
    held: bool,
    error: String,
    pub(crate) published: bool,
    stopped: bool,
    pub(crate) exit_code: Rc<Cell<i32>>,
}
impl Runtime {
    pub(crate) fn new(
        source: Source,
        status: Option<PathBuf>,
        exit_code: Rc<Cell<i32>>,
        settings: PauseSettings,
    ) -> Self {
        let auto = !matches!(source, Source::Play(_));
        Self {
            screens: BTreeMap::new(),
            gate: TopologyGate::new(vec![]),
            topology: vec![],
            next_topology: Instant::now(),
            source,
            tray: None,
            status,
            start: Instant::now(),
            paused: false,
            locked: false,
            asleep: false,
            settings,
            auto,
            power: None,
            real_power: None,
            visibility: BTreeMap::new(),
            real_visibility: BTreeMap::new(),
            next_power: Instant::now(),
            next_visibility: Instant::now(),
            #[cfg(debug_assertions)]
            fake_path: std::env::var_os("WPAPP_FAKE_SIGNALS").map(PathBuf::from),
            hold: Hold::default(),
            last_tick: SystemTime::now(),
            held: false,
            error: String::new(),
            published: false,
            stopped: false,
            exit_code,
        }
    }
    fn synthetic_signals(&self) -> bool {
        #[cfg(debug_assertions)]
        {
            self.fake_path.is_some()
        }
        #[cfg(not(debug_assertions))]
        {
            false
        }
    }
    fn signals(&self) -> Signals {
        Signals {
            user_global: self.paused,
            locked: self.locked,
            asleep: self.asleep,
            power: self.power,
            ..Signals::default()
        }
    }
    fn sample_signals(&mut self, main: MainThread) -> Result<()> {
        let now = Instant::now();
        self.locked = wallpaper_platform::session_locked(main);
        self.asleep = wallpaper_platform::displays_asleep(main);
        if now >= self.next_power {
            self.real_power = wallpaper_platform::power(main);
            self.next_power = now + Duration::from_secs(1);
        }
        let fresh_visibility = now >= self.next_visibility;
        if fresh_visibility {
            self.real_visibility = wallpaper_platform::visibility(main, &self.topology);
            self.next_visibility = now + Duration::from_millis(500);
        }
        self.power = self.real_power;
        self.visibility.clone_from(&self.real_visibility);
        #[cfg(debug_assertions)]
        if let Some(path) = &self.fake_path {
            crate::fake_signals::Fake::read(path)?.apply(
                &mut self.locked,
                &mut self.asleep,
                &mut self.power,
                &mut self.visibility,
            );
        }
        if fresh_visibility {
            for (id, screen) in &mut self.screens {
                screen.observe_visibility(self.visibility.get(id).copied().unwrap_or_default());
            }
        }
        Ok(())
    }
    fn update_decisions(&mut self, main: MainThread, immediate: bool) -> Result<bool> {
        let signals = self.signals();
        let elapsed = self.start.elapsed();
        let mut changed = false;
        for screen in self.screens.values_mut() {
            let was_paused = screen.gate.paused();
            changed |= screen.decide(&self.settings, &signals, self.auto, immediate, elapsed);
            if was_paused != screen.gate.paused() {
                screen.session.set_paused(screen.gate.paused(), elapsed);
                if let Some(desktop) = &mut screen.desktop
                    && let Err(e) = desktop.set_paused(screen.gate.paused(), main)
                {
                    screen.fail(format!("{e:#}"), main, elapsed, self.auto);
                    if !self.auto {
                        return Err(e);
                    }
                }
            }
        }
        Ok(changed)
    }
    fn is_play(&self) -> bool {
        matches!(self.source, Source::Play(_))
    }
    pub(crate) fn snapshot(&self) -> Snapshot {
        self.screens
            .values()
            .find(|s| s.info.primary)
            .or_else(|| self.screens.values().next())
            .map(|s| Snapshot::new(&s.session.state, s.gate.paused()))
            .unwrap_or_else(Snapshot::idle)
    }
    pub(crate) fn initialize(&mut self, main: MainThread) -> Result<()> {
        log::info!("backend selected={}", Desktop::BACKEND);
        let displays = wallpaper_platform::displays(main)?;
        self.gate = TopologyGate::new(displays.clone());
        self.topology = displays.clone();
        if self.is_play() && !displays.iter().any(|d| d.primary) {
            bail!("no primary display");
        }
        self.sample_signals(main)?;
        let targets = self.source.targets(&displays);
        for info in displays {
            if let Some(target) = targets.get(&info.id) {
                self.start_screen(info, target.clone(), main)?;
            }
        }
        self.next_topology = Instant::now() + Duration::from_secs(2);
        self.tray = Some(Tray::new()?);
        self.publish()
    }
    fn start_screen(&mut self, info: DisplayInfo, target: Target, main: MainThread) -> Result<()> {
        let (path, paused) = match &target {
            Target::Play { path, paused, .. } => (path.clone(), *paused),
            Target::Invalid { .. } => (String::new(), false),
        };
        let mut session = Session::new(
            info.id.0.clone(),
            path.clone(),
            Desktop::BACKEND,
            self.start.elapsed(),
        );
        if matches!(
            &target,
            Target::Play {
                kind: Kind::Image,
                ..
            }
        ) {
            session.set_still();
        }
        session.set_held(self.locked || self.asleep, self.start.elapsed());
        let id = info.id.clone();
        let mut screen = Screen {
            info,
            target,
            session,
            desktop: None,
            next_sample: Instant::now(),
            user_paused: paused,
            gate: Gate::default(),
            reasons: vec![],
            fullscreen: Stable::default(),
            covered: Stable::default(),
            stable_fullscreen: Truth::Unknown,
            stable_covered: Truth::Unknown,
            retry: Retry::default(),
            logged: None,
        };
        screen.observe_visibility(self.visibility.get(&id).copied().unwrap_or_default());
        screen.decide(
            &self.settings,
            &self.signals(),
            self.auto,
            true,
            self.start.elapsed(),
        );
        screen
            .session
            .set_paused(screen.gate.paused(), self.start.elapsed());
        let result = match &screen.target {
            Target::Invalid { reason, .. } => Err(anyhow::anyhow!("{reason}")),
            Target::Play { kind, fit, .. } => {
                let media = Media {
                    path,
                    kind: *kind,
                    fit: *fit,
                };
                Desktop::new(&id, &media, screen.gate.paused(), main).map(|mut desktop| {
                    desktop.set_restart_enabled(self.is_play());
                    screen.desktop = Some(desktop);
                    screen.session.loaded();
                })
            }
        };
        if let Err(error) = &result {
            screen.fail(
                format!("{error:#}"),
                main,
                self.start.elapsed(),
                !self.is_play(),
            );
        }
        self.screens.insert(id, screen);
        if self.is_play() {
            result?;
        }
        Ok(())
    }
    pub(crate) fn publish(&mut self) -> Result<()> {
        for screen in self.screens.values_mut() {
            let current = (screen.session.state.phase, screen.session.state.generation);
            if screen.logged != Some(current) {
                log::info!(
                    "display {} phase={:?} generation={}",
                    screen.info.id.0,
                    current.0,
                    current.1
                );
                screen.logged = Some(current);
            }
            screen.session.stamp();
        }
        if let Some(path) = &self.status {
            let bytes = wallpaper_core::render_status(StatusFile {
                schema_version: STATUS_SCHEMA,
                pid: std::process::id(),
                timestamp: wallpaper_core::timestamp(),
                error: &self.error,
                synthetic_signals: self.synthetic_signals(),
                displays: self
                    .screens
                    .values()
                    .map(|screen| DisplayStatus {
                        display: &screen.info.id,
                        name: &screen.info.name,
                        primary: screen.info.primary,
                        frame: screen.info.frame,
                        scale: screen.info.scale,
                        state: &screen.session.state,
                        paused: screen.gate.paused(),
                        paused_reasons: &screen.reasons,
                        resume_pending: screen.gate.resume_pending(),
                    })
                    .collect(),
            })?;
            wallpaper_platform::write_atomic(path, &bytes)?;
        }
        self.published = true;
        Ok(())
    }
    pub(crate) fn fail(&mut self, error: anyhow::Error) {
        log::error!("runtime failed: {}", log_reason(&format!("{error:#}")));
        self.exit_code.set(1);
        self.error = format!("{error:#}");
        if self.is_play() {
            for screen in self.screens.values_mut() {
                if screen.session.state.phase != Phase::Failed {
                    screen.session.fail(self.error.clone());
                }
            }
        }
    }
    pub(crate) fn stop(&mut self, main: MainThread) {
        if self.stopped {
            return;
        }
        self.stopped = true;
        let play = self.is_play();
        for screen in self.screens.values_mut() {
            if let Some(error) = screen.close(main) {
                log::error!("cleanup {}: {}", screen.info.id.0, log_reason(&error));
                if play {
                    self.exit_code.set(1);
                }
            }
            screen.session.stop();
        }
        if let Err(e) = self.publish() {
            self.fail(e.context("final status write"));
            if let Err(e) = self.publish() {
                log::error!("failed status write: {}", log_reason(&format!("{e:#}")));
            }
        }
        self.tray.take();
        wallpaper_platform::exit_finished();
    }
    pub(crate) fn toggle(&mut self, main: MainThread) -> Result<()> {
        self.paused = !self.paused;
        self.update_decisions(main, !self.paused)?;
        if let Some(tray) = &self.tray {
            tray.pause
                .set_text(if self.paused { "继续" } else { "暂停" });
        }
        self.publish()
    }
    fn update_topology(&mut self, main: MainThread) -> Result<bool> {
        if self.is_play() || Instant::now() < self.next_topology {
            return Ok(false);
        }
        self.next_topology = Instant::now() + Duration::from_secs(2);
        let displays = match wallpaper_platform::displays(main) {
            Ok(displays) => displays,
            Err(e) => {
                log::warn!("display enumeration: {e:#}");
                return Ok(false);
            }
        };
        let Some(displays) = self.gate.observe(displays) else {
            return Ok(false);
        };
        let added: Vec<_> = displays
            .iter()
            .filter(|d| !self.topology.iter().any(|old| old.id == d.id))
            .map(|d| &d.id.0)
            .collect();
        let removed: Vec<_> = self
            .topology
            .iter()
            .filter(|d| !displays.iter().any(|new| new.id == d.id))
            .map(|d| &d.id.0)
            .collect();
        log::info!("topology changed: added={added:?} removed={removed:?}");
        let targets = self.source.targets(&displays);
        let current = self
            .screens
            .iter()
            .map(|(id, s)| {
                (
                    id.clone(),
                    Running {
                        target: s.target.clone(),
                        frame: s.info.frame,
                        scale: s.info.scale,
                    },
                )
            })
            .collect();
        let changes = diff(&current, &displays, &targets);
        for id in changes.stop {
            if let Some(mut screen) = self.screens.remove(&id) {
                if let Some(error) = screen.close(main) {
                    log::error!("cleanup {}: {}", id.0, log_reason(&error));
                }
                screen.session.stop();
            }
        }
        self.topology = displays.clone();
        self.next_visibility = Instant::now();
        self.sample_signals(main)?;
        for id in changes.start {
            let info = displays.iter().find(|d| d.id == id).unwrap().clone();
            self.start_screen(info, targets[&id].clone(), main)?;
        }
        for id in changes.keep {
            let screen = self.screens.get_mut(&id).unwrap();
            if let Some(desktop) = &mut screen.desktop
                && let Err(e) = desktop.reposition(main)
            {
                screen.fail(
                    format!("reposition: {e:#}"),
                    main,
                    self.start.elapsed(),
                    true,
                );
            }
            screen.update_info(displays.iter().find(|d| d.id == id).unwrap().clone());
        }
        self.topology = displays;
        self.update_decisions(main, false)?;
        Ok(true)
    }
    pub(crate) fn tick(&mut self, main: MainThread) -> Result<()> {
        let wall_now = SystemTime::now();
        let gap = wall_now.duration_since(self.last_tick).unwrap_or_default();
        self.last_tick = wall_now;
        if gap > Duration::from_secs(10) {
            for screen in self.screens.values_mut() {
                if let Some(desktop) = &mut screen.desktop {
                    desktop.after_gap();
                }
            }
        }
        self.sample_signals(main)?;
        let decisions_changed = self.update_decisions(main, false)?;
        let hold = self
            .hold
            .observe(self.start.elapsed(), gap, self.locked, self.asleep);
        if self.held != hold {
            log::info!("Hold {}", if hold { "entered" } else { "exited" });
            self.held = hold;
        }
        let play = self.is_play();
        let mut changed = if hold {
            false
        } else {
            self.update_topology(main)?
        };
        changed |= decisions_changed;
        if !play && !hold {
            self.retry_screens(main)?;
        }
        for screen in self.screens.values_mut() {
            match screen.sample(main, self.start, hold) {
                Ok(sampled) => changed |= sampled,
                Err(e) => {
                    screen.fail(format!("{e:#}"), main, self.start.elapsed(), !play);
                    if play {
                        return Err(e);
                    }
                    changed = true;
                }
            }
        }
        if changed {
            self.publish()?;
        }
        Ok(())
    }
    fn retry_screens(&mut self, main: MainThread) -> Result<()> {
        let ready: Vec<_> = self
            .screens
            .iter()
            .filter(|(_, screen)| {
                screen.desktop.is_none()
                    && screen.session.state.phase == Phase::Failed
                    && screen.retry.ready(self.start.elapsed())
            })
            .map(|(id, _)| id.clone())
            .collect();
        for id in ready {
            let screen = self.screens.get_mut(&id).unwrap();
            let Target::Play {
                path, kind, fit, ..
            } = &screen.target
            else {
                continue;
            };
            let media = Media {
                path: path.clone(),
                kind: *kind,
                fit: *fit,
            };
            let paused = screen.gate.paused();
            let elapsed = self.start.elapsed();
            log::info!("display {} retry", id.0);
            screen.retry.take();
            screen.session.restart(elapsed);
            screen.session.set_paused(paused, elapsed);
            screen.next_sample = Instant::now();
            // Publish Starting before a potentially slow native constructor.
            self.publish()?;
            let result = Desktop::new(&id, &media, paused, main);
            let screen = self.screens.get_mut(&id).unwrap();
            match result {
                Ok(mut desktop) => {
                    desktop.set_restart_enabled(false);
                    screen.desktop = Some(desktop);
                    screen.session.loaded();
                }
                Err(e) => screen.fail(format!("{e:#}"), main, self.start.elapsed(), true),
            }
            self.publish()?;
        }
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use wallpaper_core::displays::Frame;

    #[test]
    fn log_reasons_preserve_diagnostics_and_redact_generic_secrets() {
        assert_eq!(log_reason("avf: Cannot Open"), "avf: Cannot Open");
        assert_eq!(
            log_reason("evidence lost: visible=no"),
            "evidence lost: visible=no"
        );
        assert_eq!(
            log_reason("avf: Cannot Open /Users/private/a b.mp4"),
            "avf: Cannot Open [redacted]"
        );
        assert_eq!(
            log_reason("load failed file:///Users/private/a b.mp4"),
            "load failed [redacted]"
        );
        assert_eq!(log_reason("failed \"/unclosed"), "reason omitted");
        assert_eq!(log_reason("failed �"), "reason omitted");
    }
    fn info(id: &str, primary: bool) -> DisplayInfo {
        DisplayInfo {
            id: DisplayId(id.into()),
            legacy_ids: vec![],
            name: id.into(),
            primary,
            frame: Frame {
                x: 0,
                y: 0,
                width: 100,
                height: 100,
            },
            scale: 1.0,
        }
    }
    fn screen(info: DisplayInfo, paused: bool) -> Screen {
        let mut gate = Gate::default();
        gate.observe(paused, true, Duration::ZERO, Duration::ZERO);
        Screen {
            session: Session::new(info.id.0.clone(), info.name.clone(), "test", Duration::ZERO),
            info,
            target: Target::Play {
                item: "video".into(),
                path: "synthetic".into(),
                kind: Kind::Video,
                fit: Fit::Fill,
                paused,
            },
            desktop: None,
            next_sample: Instant::now(),
            user_paused: paused,
            gate,
            reasons: vec![],
            fullscreen: Stable::default(),
            covered: Stable::default(),
            stable_fullscreen: Truth::Unknown,
            stable_covered: Truth::Unknown,
            retry: Retry::default(),
            logged: None,
        }
    }
    #[test]
    fn source_play_only_selects_primary_and_assignment_can_be_idle() {
        let displays = vec![info("a", false), info("z", true)];
        let targets = Source::Play("synthetic.mp4".into()).targets(&displays);
        assert_eq!(targets.len(), 1);
        assert!(
            matches!(&targets[&DisplayId("z".into())], Target::Play { path, paused: false, .. } if path == "synthetic.mp4")
        );
        let source = Source::Assigned {
            assignments: Assignments::default(),
            library: Library::default(),
            library_root: "synthetic".into(),
        };
        assert!(source.targets(&displays).is_empty());
    }
    #[test]
    fn snapshot_primary_fallback_idle_and_effective_pause() {
        let mut runtime = Runtime::new(
            Source::Play("synthetic".into()),
            None,
            Rc::new(Cell::new(0)),
            PauseSettings::default(),
        );
        assert_eq!(runtime.snapshot().phase, Phase::Stopped);
        assert_eq!(runtime.snapshot().reason, "未分配壁纸");
        assert!(runtime.snapshot().video.is_empty());
        let a = info("a", false);
        let z = info("z", true);
        runtime.screens.insert(a.id.clone(), screen(a, false));
        runtime
            .screens
            .insert(z.id.clone(), screen(z.clone(), true));
        assert_eq!(runtime.snapshot().video, "z");
        assert!(runtime.snapshot().paused);
        runtime.screens.remove(&z.id);
        assert_eq!(runtime.snapshot().video, "a");
        assert!(!runtime.snapshot().paused);
        runtime.paused = true;
        assert!(!runtime.snapshot().paused);
        runtime.screens.values_mut().next().unwrap().gate.observe(
            true,
            true,
            Duration::ZERO,
            Duration::ZERO,
        );
        assert!(runtime.snapshot().paused);
    }

    #[test]
    fn retries_are_per_screen_and_exhaustion_preserves_failure_reason() {
        let mut a = screen(info("a", true), false);
        let mut b = screen(info("b", false), true);
        b.session.sample(ready(0.), Duration::ZERO);
        b.session.sample(ready(1.), Duration::from_secs(2));
        for n in 0..4 {
            a.session.fail("synthetic failure; cleanup: detail");
            a.schedule_retry(Duration::from_secs(n * 20), true);
            assert_eq!(a.session.state.phase, Phase::Failed);
            assert_eq!(a.retry.ready(Duration::from_secs(100)), n < 3);
            assert_eq!(
                a.session.state.reason.ends_with("; retries exhausted"),
                n == 3
            );
            assert!(
                a.session
                    .state
                    .reason
                    .starts_with("synthetic failure; cleanup: detail")
            );
            a.retry.take();
        }
        assert_eq!(b.session.state.phase, Phase::Confirmed);
        assert_eq!(b.session.state.generation, 1);
        assert!(!b.retry.ready(Duration::from_secs(100)));
    }

    #[test]
    fn play_mode_and_invalid_targets_do_not_schedule_retries() {
        let mut s = screen(info("a", true), false);
        s.session.fail("synthetic");
        s.schedule_retry(Duration::ZERO, false);
        assert!(!s.retry.ready(Duration::from_secs(100)));
        assert_eq!(s.session.state.reason, "synthetic");
        s.target = Target::Invalid {
            item: "missing".into(),
            reason: "missing item".into(),
        };
        s.schedule_retry(Duration::ZERO, true);
        assert!(!s.retry.ready(Duration::from_secs(100)));
        assert_eq!(s.session.state.reason, "synthetic");
    }

    #[test]
    fn decisions_include_missing_desktops_and_manual_resume_keeps_other_reasons() {
        let mut s = screen(info("a", true), false);
        let cfg = PauseSettings::default();
        let mut global = Signals {
            user_global: true,
            locked: true,
            ..Signals::default()
        };
        s.decide(&cfg, &global, true, false, Duration::ZERO);
        assert!(s.desktop.is_none());
        assert!(s.gate.paused());
        assert_eq!(s.reasons, [Reason::User, Reason::Locked]);
        global.user_global = false;
        s.decide(&cfg, &global, true, true, Duration::ZERO);
        assert!(s.gate.paused());
        assert!(!s.gate.resume_pending());
        assert_eq!(s.reasons, [Reason::Locked]);
        global.locked = false;
        s.decide(&cfg, &global, true, false, Duration::ZERO);
        assert!(s.gate.paused() && s.gate.resume_pending());
        assert!(s.reasons.is_empty());
        s.decide(&cfg, &global, true, false, Duration::from_millis(300));
        assert!(!s.gate.paused());
        global.user_global = true;
        s.decide(&cfg, &global, true, false, Duration::from_secs(1));
        global.user_global = false;
        s.decide(&cfg, &global, true, true, Duration::from_secs(1));
        assert!(!s.gate.paused());
    }
    #[test]
    fn play_ignores_all_automatic_and_display_pause_reasons() {
        let mut s = screen(info("a", true), true);
        s.stable_fullscreen = Truth::Yes;
        s.stable_covered = Truth::Yes;
        let mut global = Signals {
            locked: true,
            asleep: true,
            power: Some(Power {
                on_battery: true,
                percent: Some(0),
            }),
            ..Signals::default()
        };
        s.decide(
            &PauseSettings::default(),
            &global,
            false,
            true,
            Duration::ZERO,
        );
        assert!(s.reasons.is_empty());
        assert!(!s.gate.paused());
        global.user_global = true;
        s.decide(
            &PauseSettings::default(),
            &global,
            false,
            true,
            Duration::ZERO,
        );
        assert_eq!(s.reasons, [Reason::User]);
        assert!(s.gate.paused());
    }
    #[test]
    fn frame_change_resets_stability_and_decision_ticks_do_not_count_samples() {
        let mut s = screen(info("a", true), false);
        let visibility = wallpaper_platform::Visibility {
            fullscreen: Truth::Yes,
            covered: Truth::Yes,
            coverage: Some(1.0),
        };
        s.observe_visibility(visibility);
        for ms in [0, 100, 200, 300, 400] {
            s.decide(
                &PauseSettings::default(),
                &Signals::default(),
                true,
                false,
                Duration::from_millis(ms),
            );
        }
        assert!(s.reasons.is_empty());
        s.observe_visibility(visibility);
        s.observe_visibility(visibility);
        assert_eq!(s.stable_covered, Truth::Yes);
        s.update_info(s.info.clone());
        assert_eq!(s.stable_covered, Truth::Yes);
        let mut info = s.info.clone();
        info.frame.x = 100;
        s.update_info(info);
        assert_eq!(s.stable_covered, Truth::Unknown);
        assert_eq!(s.stable_fullscreen, Truth::Unknown);
        s.observe_visibility(visibility);
        s.observe_visibility(visibility);
        assert_eq!(s.stable_covered, Truth::No);
        assert_eq!(s.stable_fullscreen, Truth::No);
        s.observe_visibility(visibility);
        assert_eq!(s.stable_covered, Truth::Yes);
        s.observe_visibility(wallpaper_platform::Visibility::default());
        assert_eq!(s.stable_covered, Truth::Unknown);
    }

    fn ready(position: f64) -> Evidence {
        Evidence {
            position: Some(position),
            visible: wallpaper_core::Truth::Yes,
            attached: wallpaper_core::Truth::Yes,
            on_screen: wallpaper_core::Truth::Yes,
            ready_for_display: wallpaper_core::Truth::Yes,
            ..Evidence::default()
        }
    }
}
