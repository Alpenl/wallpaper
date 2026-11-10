use std::time::Duration;

/// A per-screen retry budget, renewed only by sustained confirmation.
#[derive(Default)]
pub struct Retry {
    attempts: usize,
    pending: Option<Duration>,
    confirmed_since: Option<Duration>,
}
impl Retry {
    pub fn on_failure(&mut self, now: Duration) -> bool {
        self.confirmed_since = None;
        self.pending = None;
        let Some(seconds) = [2, 5, 15].get(self.attempts) else {
            return false;
        };
        self.attempts += 1;
        self.pending = Some(now + Duration::from_secs(*seconds));
        true
    }
    pub fn on_confirmed(&mut self, now: Duration) {
        let since = *self.confirmed_since.get_or_insert(now);
        if now.saturating_sub(since) >= Duration::from_secs(60) {
            self.attempts = 0;
        }
    }
    pub fn ready(&self, now: Duration) -> bool {
        self.pending.is_some_and(|due| now >= due)
    }
    pub fn take(&mut self) {
        self.pending = None;
    }
}

/// Suppress evidence failures during lock, display sleep, and resume grace.
#[derive(Default)]
pub struct Hold {
    until: Duration,
}
impl Hold {
    pub fn observe(
        &mut self,
        now: Duration,
        wall_gap: Duration,
        locked: bool,
        asleep: bool,
    ) -> bool {
        if wall_gap > Duration::from_secs(10) {
            self.until = now + Duration::from_secs(5);
        }
        locked || asleep || now < self.until
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn sec(n: u64) -> Duration {
        Duration::from_secs(n)
    }

    #[test]
    fn delays_exhaustion_and_ready_take() {
        let mut retry = Retry::default();
        assert!(!retry.ready(sec(100)));
        for (now, delay) in [(0, 2), (10, 5), (20, 15)] {
            assert!(retry.on_failure(sec(now)));
            assert!(!retry.ready(sec(now + delay) - Duration::from_nanos(1)));
            assert!(retry.ready(sec(now + delay)));
            assert!(retry.ready(sec(now + delay + 1)));
            retry.take();
            assert!(!retry.ready(sec(100)));
        }
        assert!(!retry.on_failure(sec(40)));
        assert!(!retry.ready(sec(100)));
        assert!(!retry.on_failure(sec(100)));
    }

    #[test]
    fn failure_within_59_seconds_preserves_budget() {
        let mut retry = Retry::default();
        assert!(retry.on_failure(sec(0)));
        retry.take();
        retry.on_confirmed(sec(10));
        retry.on_confirmed(sec(69));
        assert!(retry.on_failure(sec(69)));
        assert!(!retry.ready(sec(71)));
        assert!(retry.ready(sec(74)));
    }

    #[test]
    fn full_60_seconds_renews_exhausted_budget() {
        let mut retry = Retry::default();
        for n in 0..3 {
            assert!(retry.on_failure(sec(n)));
            retry.take();
        }
        retry.on_confirmed(sec(10));
        retry.on_confirmed(sec(70));
        assert!(retry.on_failure(sec(70)));
        assert!(!retry.ready(sec(71)));
        assert!(retry.ready(sec(72)));
    }

    #[test]
    fn interrupted_confirmation_starts_timer_again() {
        let mut retry = Retry::default();
        assert!(retry.on_failure(sec(0)));
        retry.take();
        retry.on_confirmed(sec(10));
        retry.on_confirmed(sec(69));
        assert!(retry.on_failure(sec(69)));
        retry.take();
        retry.on_confirmed(sec(74));
        retry.on_confirmed(sec(133));
        assert!(retry.on_failure(sec(133)));
        assert!(!retry.ready(sec(138)));
        assert!(retry.ready(sec(148)));
        retry.take();
        retry.on_confirmed(sec(150));
        retry.on_confirmed(sec(210));
        assert!(retry.on_failure(sec(210)));
        assert!(retry.ready(sec(212)));
    }

    #[test]
    fn lock_and_sleep_hold_only_while_observed() {
        let mut hold = Hold::default();
        assert!(!hold.observe(sec(0), sec(0), false, false));
        assert!(hold.observe(sec(1), sec(1), true, false));
        assert!(!hold.observe(sec(2), sec(1), false, false));
        assert!(hold.observe(sec(3), sec(1), false, true));
        assert!(hold.observe(sec(4), sec(1), true, true));
        assert!(!hold.observe(sec(5), sec(1), false, false));
    }

    #[test]
    fn suspension_threshold_and_grace_boundary() {
        let mut hold = Hold::default();
        assert!(!hold.observe(sec(10), sec(10), false, false));
        assert!(hold.observe(sec(20), sec(10) + Duration::from_nanos(1), false, false));
        assert!(hold.observe(sec(25) - Duration::from_nanos(1), sec(1), false, false));
        assert!(!hold.observe(sec(25), sec(0), false, false));
    }

    #[test]
    fn another_gap_renews_grace_even_while_locked_or_asleep() {
        let mut hold = Hold::default();
        assert!(hold.observe(sec(10), sec(11), true, true));
        assert!(hold.observe(sec(12), sec(11), false, false));
        assert!(hold.observe(sec(15), sec(1), false, false));
        assert!(!hold.observe(sec(17), sec(1), false, false));
    }
}
