use crate::{
    Truth,
    config::{BatteryPolicy, PauseSettings},
    displays::Frame,
};
use serde::{Deserialize, Serialize};
use std::time::Duration;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
pub struct Power {
    pub on_battery: bool,
    pub percent: Option<u8>,
}
#[derive(Debug, Default, Clone)]
pub struct Signals {
    pub user_global: bool,
    pub user_display: bool,
    pub locked: bool,
    pub asleep: bool,
    pub power: Option<Power>,
    pub fullscreen: Truth,
    pub covered: Truth,
}
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub enum Reason {
    User,
    UserDisplay,
    Locked,
    Asleep,
    Battery,
    LowBattery,
    Fullscreen,
    Covered,
}
pub fn reasons(settings: &PauseSettings, s: &Signals) -> Vec<Reason> {
    let battery = s.power.filter(|p| p.on_battery);
    [
        (s.user_global, Reason::User),
        (s.user_display, Reason::UserDisplay),
        (s.locked, Reason::Locked),
        (s.asleep, Reason::Asleep),
        (
            settings.battery_policy == BatteryPolicy::Pause && battery.is_some(),
            Reason::Battery,
        ),
        (
            battery
                .and_then(|p| p.percent)
                .is_some_and(|p| p <= settings.low_battery_pct),
            Reason::LowBattery,
        ),
        (
            settings.on_fullscreen && s.fullscreen == Truth::Yes,
            Reason::Fullscreen,
        ),
        (
            settings.on_covered && s.covered == Truth::Yes,
            Reason::Covered,
        ),
    ]
    .into_iter()
    .filter_map(|(active, reason)| active.then_some(reason))
    .collect()
}
#[derive(Debug, Default)]
pub struct Gate {
    paused: bool,
    clear_since: Option<Duration>,
}
impl Gate {
    pub fn paused(&self) -> bool {
        self.paused
    }
    pub fn resume_pending(&self) -> bool {
        self.paused && self.clear_since.is_some()
    }
    /// Returns the final pause target, not whether it changed.
    pub fn observe(
        &mut self,
        wanted: bool,
        immediate: bool,
        now: Duration,
        debounce: Duration,
    ) -> bool {
        if wanted {
            self.paused = true;
            self.clear_since = None;
        } else if immediate {
            self.paused = false;
            self.clear_since = None;
        } else if self.paused {
            let since = *self.clear_since.get_or_insert(now);
            if now.saturating_sub(since) >= debounce {
                self.paused = false;
                self.clear_since = None;
            }
        }
        self.paused
    }
}
#[derive(Debug, Default)]
pub struct Stable {
    run: u8,
}
impl Stable {
    pub fn observe(&mut self, sample: Truth) -> Truth {
        if sample != Truth::Yes {
            self.reset();
            return sample;
        }
        self.run = (self.run + 1).min(3);
        if self.run == 3 { Truth::Yes } else { Truth::No }
    }
    pub fn reset(&mut self) {
        self.run = 0;
    }
}
#[derive(Debug, Clone, Copy)]
pub struct Rect {
    pub x: i64,
    pub y: i64,
    pub width: i64,
    pub height: i64,
}
impl From<Frame> for Rect {
    fn from(f: Frame) -> Self {
        Self {
            x: f.x.into(),
            y: f.y.into(),
            width: f.width.into(),
            height: f.height.into(),
        }
    }
}
// Widen before addition/subtraction. Even extreme i64 origins and dimensions
// have exact endpoints and union areas in i128 (positive dimensions <= i64::MAX).
#[derive(Clone, Copy)]
struct Bounds {
    x0: i128,
    y0: i128,
    x1: i128,
    y1: i128,
}
impl Bounds {
    fn of(r: Rect) -> Option<Self> {
        (r.width > 0 && r.height > 0).then_some(Self {
            x0: r.x.into(),
            y0: r.y.into(),
            x1: i128::from(r.x) + i128::from(r.width),
            y1: i128::from(r.y) + i128::from(r.height),
        })
    }
    fn inset(mut self, tolerance: i64) -> Option<Self> {
        let t = i128::from(tolerance.max(0));
        self.x0 += t;
        self.y0 += t;
        self.x1 -= t;
        self.y1 -= t;
        (self.x0 < self.x1 && self.y0 < self.y1).then_some(self)
    }
    fn area(self) -> i128 {
        (self.x1 - self.x0) * (self.y1 - self.y0)
    }
}
fn union(area: Bounds, windows: &[Rect]) -> i128 {
    let clipped: Vec<_> = windows
        .iter()
        .filter_map(|r| {
            let r = Bounds::of(*r)?;
            let r = Bounds {
                x0: r.x0.max(area.x0),
                y0: r.y0.max(area.y0),
                x1: r.x1.min(area.x1),
                y1: r.y1.min(area.y1),
            };
            (r.x0 < r.x1 && r.y0 < r.y1).then_some(r)
        })
        .collect();
    let mut xs: Vec<_> = clipped.iter().flat_map(|r| [r.x0, r.x1]).collect();
    xs.sort_unstable();
    xs.dedup();
    let mut total = 0;
    for x in xs.windows(2) {
        let mut ys: Vec<_> = clipped
            .iter()
            .filter(|r| r.x0 <= x[0] && r.x1 >= x[1])
            .map(|r| (r.y0, r.y1))
            .collect();
        ys.sort_unstable();
        let mut end = area.y0;
        let mut height = 0;
        for (lo, hi) in ys {
            height += (hi - lo.max(end)).max(0);
            end = end.max(hi);
        }
        total += (x[1] - x[0]) * height;
    }
    total
}
pub fn covered_fraction(area: Rect, windows: &[Rect]) -> f64 {
    Bounds::of(area).map_or(0.0, |a| union(a, windows) as f64 / a.area() as f64)
}
/// A degenerate area, including one collapsed by tolerance, is not covered.
pub fn fully_covered(area: Rect, windows: &[Rect], tolerance: i64) -> bool {
    Bounds::of(area)
        .and_then(|a| a.inset(tolerance))
        .is_some_and(|a| union(a, windows) == a.area())
}
pub fn contains(outer: Rect, inner: Rect, tolerance: i64) -> bool {
    let (Some(o), Some(i)) = (
        Bounds::of(outer),
        Bounds::of(inner).and_then(|i| i.inset(tolerance)),
    ) else {
        return false;
    };
    o.x0 <= i.x0 && o.y0 <= i.y0 && o.x1 >= i.x1 && o.y1 >= i.y1
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn all_reasons_independent_and_ordered() {
        let settings = PauseSettings::default();
        let mut s = Signals {
            user_global: true,
            user_display: true,
            locked: true,
            asleep: true,
            power: Some(Power {
                on_battery: true,
                percent: Some(20),
            }),
            fullscreen: Truth::Yes,
            covered: Truth::Yes,
        };
        let all = vec![
            Reason::User,
            Reason::UserDisplay,
            Reason::Locked,
            Reason::Asleep,
            Reason::Battery,
            Reason::LowBattery,
            Reason::Fullscreen,
            Reason::Covered,
        ];
        assert_eq!(reasons(&settings, &s), all);
        for i in 0..8 {
            let mut single = Signals::default();
            let mut cfg = settings.clone();
            match i {
                0 => {
                    single.user_global = true;
                    s.user_global = false;
                }
                1 => {
                    single.user_display = true;
                    s.user_display = false;
                }
                2 => {
                    single.locked = true;
                    s.locked = false;
                }
                3 => {
                    single.asleep = true;
                    s.asleep = false;
                }
                4 => {
                    single.power = Some(Power {
                        on_battery: true,
                        percent: None,
                    });
                    s.power.as_mut().unwrap().percent = Some(20);
                }
                5 => {
                    cfg.battery_policy = BatteryPolicy::Ignore;
                    single.power = s.power;
                    s.power = None;
                }
                6 => {
                    single.fullscreen = Truth::Yes;
                    s.fullscreen = Truth::No;
                }
                _ => {
                    single.covered = Truth::Yes;
                    s.covered = Truth::No;
                }
            }
            assert_eq!(reasons(&cfg, &single), vec![all[i]]);
            assert!(reasons(&cfg, &Signals::default()).is_empty());
            if i != 4 {
                assert_eq!(reasons(&settings, &s), all[i + 1..]);
            } else {
                let mut ignore = settings.clone();
                ignore.battery_policy = BatteryPolicy::Ignore;
                assert_eq!(reasons(&ignore, &s), all[i + 1..]);
            }
        }
    }
    #[test]
    fn missing_disabled_and_ac_signals() {
        let mut cfg = PauseSettings::default();
        let mut s = Signals::default();
        assert!(reasons(&cfg, &s).is_empty());
        s.power = Some(Power {
            on_battery: false,
            percent: Some(0),
        });
        assert!(reasons(&cfg, &s).is_empty());
        s.power = Some(Power {
            on_battery: true,
            percent: None,
        });
        assert_eq!(reasons(&cfg, &s), [Reason::Battery]);
        cfg.battery_policy = BatteryPolicy::Ignore;
        assert!(reasons(&cfg, &s).is_empty());
        s.power.as_mut().unwrap().percent = Some(0);
        assert_eq!(reasons(&cfg, &s), [Reason::LowBattery]);
        cfg.on_fullscreen = false;
        cfg.on_covered = false;
        s.fullscreen = Truth::Yes;
        s.covered = Truth::Yes;
        s.power = None;
        assert!(reasons(&cfg, &s).is_empty());
    }
    #[test]
    fn fast_stop_slow_resume_and_interrupt() {
        let mut g = Gate::default();
        let d = Duration::from_millis(300);
        assert!(!g.resume_pending());
        assert!(g.observe(true, true, Duration::ZERO, d));
        assert!(!g.resume_pending());
        assert!(g.observe(false, false, Duration::ZERO, d));
        assert!(g.resume_pending());
        assert!(g.observe(false, false, Duration::from_millis(299), d));
        assert!(!g.observe(false, false, d, d));
        assert!(!g.resume_pending());
        g.observe(true, false, d, d);
        g.observe(false, false, d, d);
        g.observe(true, false, d + d, d);
        assert!(!g.resume_pending());
        assert!(g.observe(false, false, d + d, d));
        assert!(!g.observe(false, true, d + d, d));
    }
    #[test]
    fn stable_only_consecutive_yes_and_unknown_clears() {
        let mut s = Stable::default();
        for sample in [Truth::Yes, Truth::Yes, Truth::No, Truth::Yes, Truth::Yes] {
            assert_eq!(s.observe(sample), Truth::No);
        }
        assert_eq!(s.observe(Truth::Unknown), Truth::Unknown);
        assert_eq!(s.observe(Truth::Yes), Truth::No);
        assert_eq!(s.observe(Truth::Yes), Truth::No);
        assert_eq!(s.observe(Truth::Yes), Truth::Yes);
        assert_eq!(s.observe(Truth::Yes), Truth::Yes);
        assert_eq!(s.observe(Truth::No), Truth::No);
        s.observe(Truth::Yes);
        s.observe(Truth::Yes);
        s.reset();
        assert_eq!(s.observe(Truth::Yes), Truth::No);
    }
    fn r(x: i64, y: i64, width: i64, height: i64) -> Rect {
        Rect {
            x,
            y,
            width,
            height,
        }
    }
    #[test]
    fn exact_union_clips_overlaps_and_narrow_gaps() {
        let a = r(0, 0, 100, 100);
        assert!(!fully_covered(a, &[r(0, 0, 49, 100), r(50, 0, 50, 100)], 0));
        assert!(fully_covered(a, &[r(0, 0, 50, 100), r(50, 0, 50, 100)], 0));
        assert_eq!(
            covered_fraction(a, &[r(-50, -50, 100, 200), r(25, 0, 50, 100)]),
            0.75
        );
        assert_eq!(covered_fraction(a, &[r(-100, -100, 300, 300)]), 1.0);
        assert_eq!(covered_fraction(a, &[]), 0.0);
        assert!(!fully_covered(a, &[], 0));
    }
    #[test]
    fn tolerance_degenerate_and_extreme_coordinates() {
        let a = r(0, 0, 100, 100);
        let w = r(2, 2, 96, 96);
        assert!(contains(w, a, 2));
        assert!(!contains(w, a, 1));
        assert!(fully_covered(a, &[w], 2));
        assert!(!fully_covered(a, &[w], 1));
        for a in [r(0, 0, 0, 1), r(0, 0, 1, -1)] {
            assert_eq!(covered_fraction(a, &[w]), 0.0);
            assert!(!fully_covered(a, &[w], 0));
            assert!(!contains(w, a, 0));
        }
        assert!(!fully_covered(r(0, 0, 4, 4), &[a], 2));
        assert_eq!(covered_fraction(a, &[r(0, 0, -1, 100)]), 0.0);
        for x in [i64::MIN, i64::MAX] {
            let a = r(x, x, i64::MAX, i64::MAX);
            assert!(fully_covered(a, &[a, a], 0));
            assert_eq!(covered_fraction(a, &[a, a]), 1.0);
            assert!(contains(a, a, 0));
        }
    }
}
