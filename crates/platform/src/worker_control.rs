use std::time::Duration;

/// One serialized request stream: control targets wait behind the current reply,
/// then bypass the periodic sample deadline. New targets coalesce to the latest.
#[derive(Default)]
pub(crate) struct Control {
    pending: Option<bool>,
    deadline: Option<Duration>,
    gap_used: bool,
}
impl Control {
    pub fn target(&mut self, paused: bool) {
        self.pending = Some(paused);
    }
    pub fn pending(&self) -> Option<bool> {
        self.pending
    }
    pub fn sent(&mut self, now: Duration, timeout: Duration) {
        self.pending = None;
        self.deadline = Some(now.saturating_add(timeout));
        self.gap_used = false;
    }
    pub fn replied(&mut self) {
        self.deadline = None;
    }
    pub fn after_gap(&mut self, now: Duration) {
        if self.deadline.is_some() && !self.gap_used {
            self.deadline = Some(now.saturating_add(Duration::from_secs(15)));
            self.gap_used = true;
        }
    }
    pub fn expired(&self, now: Duration) -> bool {
        self.deadline.is_some_and(|d| now >= d)
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn pending_target_survives_reply_and_coalesces() {
        let mut c = Control::default();
        c.sent(Duration::ZERO, Duration::from_secs(3));
        c.target(true);
        c.target(false);
        c.target(true);
        c.replied();
        assert_eq!(c.pending(), Some(true));
        c.sent(Duration::from_secs(1), Duration::from_secs(3));
        assert_eq!(c.pending(), None);
        assert!(!c.expired(Duration::from_secs(3)));
        assert!(c.expired(Duration::from_secs(4)));
    }
    #[test]
    fn gap_grace_is_once_per_request_and_bounded() {
        let mut c = Control::default();
        c.after_gap(Duration::from_secs(100));
        c.sent(Duration::ZERO, Duration::from_secs(3));
        c.after_gap(Duration::from_secs(100));
        c.after_gap(Duration::from_secs(110));
        assert!(!c.expired(Duration::from_secs(114)));
        assert!(c.expired(Duration::from_secs(115)));
        c.replied();
        assert!(!c.expired(Duration::from_secs(200)));
        c.sent(Duration::from_secs(200), Duration::from_secs(3));
        c.after_gap(Duration::from_secs(300));
        assert!(c.expired(Duration::from_secs(315)));
    }
}
