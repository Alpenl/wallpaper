use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;

pub const SCHEMA_VERSION: u32 = 1;

#[derive(Debug, Clone, PartialEq, Eq, PartialOrd, Ord, Serialize, Deserialize)]
#[serde(transparent)]
pub struct DisplayId(pub String);

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Assignments {
    pub schema_version: u32,
    pub default: Option<String>,
    pub displays: BTreeMap<DisplayId, Assignment>,
    pub aliases: BTreeMap<DisplayId, String>,
}

impl Default for Assignments {
    fn default() -> Self {
        Self {
            schema_version: SCHEMA_VERSION,
            default: None,
            displays: BTreeMap::new(),
            aliases: BTreeMap::new(),
        }
    }
}

impl Assignments {
    pub fn remove_item(&mut self, id: &str) {
        if self.default.as_deref() == Some(id) {
            self.default = None;
        }
        for assignment in self.displays.values_mut() {
            if assignment.item.as_deref() == Some(id) {
                assignment.item = None;
            }
        }
    }
    pub fn assign_display(&mut self, display: DisplayId, item: Option<String>) {
        self.displays.entry(display).or_default().item = item;
    }
    pub fn assign_all(&mut self, item: Option<String>) {
        self.default = item.clone();
        for assignment in self.displays.values_mut() {
            assignment.item = item.clone();
        }
    }
}

#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Assignment {
    pub item: Option<String>,
    pub paused: bool,
}

use crate::library::{Fit, Kind, Library};
use std::path::Path;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
pub struct Frame {
    pub x: i32,
    pub y: i32,
    pub width: u32,
    pub height: u32,
}

#[derive(Debug, Clone, PartialEq, Serialize)]
pub struct DisplayInfo {
    pub id: DisplayId,
    pub legacy_ids: Vec<DisplayId>,
    pub name: String,
    pub primary: bool,
    pub frame: Frame,
    pub scale: f64,
}

pub fn sort_by_layout(displays: &mut [DisplayInfo]) {
    displays.sort_by_key(|d| (d.frame.x, d.frame.y));
}

// Call after sort_by_layout, exactly as desktop.Dedupe does in the Go host.
pub fn dedupe(displays: &mut [DisplayInfo]) {
    let mut seen = BTreeMap::new();
    for display in displays {
        let count = seen.entry(display.id.clone()).or_insert(0);
        *count += 1;
        if *count > 1 {
            display.id.0 = format!("{}#{}", display.id.0, count);
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Target {
    Play {
        item: String,
        path: String,
        kind: Kind,
        fit: Fit,
        paused: bool,
    },
    Invalid {
        item: String,
        reason: String,
    },
}

pub fn resolve(
    assignments: &Assignments,
    library: &Library,
    library_root: &Path,
    displays: &[DisplayInfo],
) -> BTreeMap<DisplayId, Target> {
    displays
        .iter()
        .filter_map(|display| {
            let assignment = assignments.displays.get(&display.id).or_else(|| {
                display
                    .legacy_ids
                    .iter()
                    .find_map(|id| assignments.displays.get(id))
            });
            let item = assignment
                .and_then(|a| a.item.as_ref())
                .or(assignments.default.as_ref())?;
            let invalid = |reason: String| Target::Invalid {
                item: item.clone(),
                reason,
            };
            let target = match library.find(item) {
                None => invalid(format!("library item not found: {item}")),
                Some(entry) if !crate::library::is_safe_component(&entry.id) => {
                    invalid(format!("unsafe library id: {item}"))
                }
                Some(entry) if entry.entry.is_empty() => {
                    invalid(format!("library item has empty entry: {item}"))
                }
                Some(entry) => {
                    let path = Path::new(&entry.entry);
                    let path = if path.is_absolute() {
                        path.to_path_buf()
                    } else {
                        library_root.join(&entry.id).join(path)
                    };
                    Target::Play {
                        item: item.clone(),
                        kind: entry.kind,
                        fit: entry.fit,
                        path: path.to_string_lossy().into_owned(),
                        paused: assignment.is_some_and(|a| a.paused),
                    }
                }
            };
            Some((display.id.clone(), target))
        })
        .collect()
}

pub struct Running {
    pub target: Target,
    pub frame: Frame,
    pub scale: f64,
}

#[derive(Debug, Default, PartialEq, Eq)]
pub struct Changes {
    pub stop: Vec<DisplayId>,
    pub start: Vec<DisplayId>,
    pub keep: Vec<DisplayId>,
}

pub fn diff(
    current: &BTreeMap<DisplayId, Running>,
    displays: &[DisplayInfo],
    targets: &BTreeMap<DisplayId, Target>,
) -> Changes {
    let mut changes = Changes::default();
    for id in current.keys() {
        if !targets.contains_key(id) || !displays.iter().any(|d| &d.id == id) {
            changes.stop.push(id.clone());
        }
    }
    for (id, target) in targets {
        let Some(display) = displays.iter().find(|d| &d.id == id) else {
            continue;
        };
        match current.get(id) {
            None => changes.start.push(id.clone()),
            Some(running)
                if &running.target != target
                    || running.frame.width != display.frame.width
                    || running.frame.height != display.frame.height
                    || running.scale != display.scale =>
            {
                changes.stop.push(id.clone());
                changes.start.push(id.clone());
            }
            Some(_) => changes.keep.push(id.clone()),
        }
    }
    changes
}

pub struct TopologyGate {
    applied: Vec<DisplayInfo>,
    candidate: Option<Vec<DisplayInfo>>,
}
impl TopologyGate {
    pub fn new(initial: Vec<DisplayInfo>) -> Self {
        Self {
            applied: initial,
            candidate: None,
        }
    }
    pub fn observe(&mut self, list: Vec<DisplayInfo>) -> Option<Vec<DisplayInfo>> {
        if list.is_empty() {
            return None;
        }
        if list == self.applied {
            self.candidate = None;
            return None;
        }
        if self.candidate.as_ref() == Some(&list) {
            self.applied = list.clone();
            self.candidate = None;
            return Some(list);
        }
        self.candidate = Some(list);
        None
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::library::{Fit, Item};

    #[test]
    fn assignment_edits_preserve_pause_and_unrelated_items() {
        let mut a = Assignments::default();
        a.assign_display(DisplayId("a".into()), Some("one".into()));
        a.displays.get_mut(&DisplayId("a".into())).unwrap().paused = true;
        a.assign_display(DisplayId("a".into()), Some("two".into()));
        a.assign_display(DisplayId("b".into()), Some("other".into()));
        a.default = Some("two".into());
        a.remove_item("two");
        assert_eq!(a.default, None);
        assert_eq!(
            a.displays[&DisplayId("a".into())],
            Assignment {
                item: None,
                paused: true
            }
        );
        assert_eq!(
            a.displays[&DisplayId("b".into())].item.as_deref(),
            Some("other")
        );
        a.assign_all(Some("all".into()));
        assert_eq!(a.default.as_deref(), Some("all"));
        assert!(
            a.displays
                .values()
                .all(|v| v.item.as_deref() == Some("all"))
        );
        a.remove_item("absent");
        assert_eq!(a.default.as_deref(), Some("all"));
        a.assign_all(None);
        assert!(a.displays[&DisplayId("a".into())].paused);
        assert!(!a.displays[&DisplayId("b".into())].paused);
        assert!(a.displays.values().all(|v| v.item.is_none()));
    }
    fn display(id: &str) -> DisplayInfo {
        DisplayInfo {
            id: DisplayId(id.into()),
            legacy_ids: vec![DisplayId("old".into()), DisplayId("older".into())],
            name: "Screen".into(),
            primary: true,
            frame: Frame {
                x: 0,
                y: 0,
                width: 1920,
                height: 1080,
            },
            scale: 1.0,
        }
    }
    fn library() -> Library {
        Library {
            items: vec![Item {
                id: "video".into(),
                title: "Video".into(),
                kind: Kind::Video,
                entry: "clip.mp4".into(),
                width: None,
                height: None,
                duration_sec: None,
                size_bytes: None,
                added_at: String::new(),
                fit: Fit::Fill,
            }],
            ..Library::default()
        }
    }
    fn play() -> Target {
        Target::Play {
            item: "video".into(),
            kind: Kind::Video,
            fit: Fit::Fill,
            path: Path::new("root")
                .join("video")
                .join("clip.mp4")
                .to_string_lossy()
                .into_owned(),
            paused: false,
        }
    }
    fn resolved(assignments: &Assignments, lib: &Library) -> Option<Target> {
        resolve(assignments, lib, Path::new("root"), &[display("a")]).remove(&DisplayId("a".into()))
    }
    #[test]
    fn resolve_id_then_legacy_in_order() {
        let mut assignments = Assignments::default();
        for id in ["a", "old", "older"] {
            assignments.displays.insert(
                DisplayId(id.into()),
                Assignment {
                    item: Some(id.into()),
                    paused: true,
                },
            );
        }
        for id in ["a", "old", "older"] {
            assert!(
                matches!(resolved(&assignments, &library()), Some(Target::Invalid { item, .. }) if item == id)
            );
            assignments.displays.remove(&DisplayId(id.into()));
        }
    }
    #[test]
    fn resolve_direct_and_legacy_play_pause() {
        for id in ["a", "old", "older"] {
            let mut assignments = Assignments {
                default: Some("missing".into()),
                ..Assignments::default()
            };
            assignments.displays.insert(
                DisplayId(id.into()),
                Assignment {
                    item: Some("video".into()),
                    paused: true,
                },
            );
            assert!(
                matches!(resolved(&assignments, &library()), Some(Target::Play { item, paused: true, .. }) if item == "video")
            );
        }
    }
    #[test]
    fn resolve_default_and_none_with_pause_from_matching_entry() {
        let mut assignments = Assignments::default();
        assert_eq!(resolved(&assignments, &library()), None);
        assignments.default = Some("video".into());
        assert_eq!(resolved(&assignments, &library()), Some(play()));
        assignments.displays.insert(
            DisplayId("a".into()),
            Assignment {
                item: None,
                paused: true,
            },
        );
        assignments.displays.insert(
            DisplayId("old".into()),
            Assignment {
                item: Some("missing".into()),
                paused: false,
            },
        );
        assert!(
            matches!(resolved(&assignments, &library()), Some(Target::Play { paused: true, item, .. }) if item == "video")
        );
        assignments.default = None;
        assert_eq!(resolved(&assignments, &library()), None);
    }
    #[test]
    fn resolve_paths_and_invalid_reasons() {
        let assignments = Assignments {
            default: Some("video".into()),
            ..Assignments::default()
        };
        let mut lib = library();
        assert_eq!(resolved(&assignments, &lib), Some(play()));
        // Absolute on each target OS; no filesystem access.
        let absolute = if cfg!(windows) {
            "C:\\media\\clip.mp4"
        } else {
            "/media/clip.mp4"
        };
        lib.items[0].entry = absolute.into();
        assert!(
            matches!(resolved(&assignments, &lib), Some(Target::Play { path, .. }) if path == absolute)
        );
        lib.items[0].entry.clear();
        assert!(
            matches!(resolved(&assignments, &lib), Some(Target::Invalid { reason, .. }) if reason.contains("empty entry"))
        );
        lib.items[0].kind = Kind::Image;
        lib.items[0].entry = "still.png".into();
        lib.items[0].fit = Fit::Fit;
        assert!(matches!(
            resolved(&assignments, &lib),
            Some(Target::Play {
                kind: Kind::Image,
                fit: Fit::Fit,
                ..
            })
        ));
        lib.items.clear();
        assert!(
            matches!(resolved(&assignments, &lib), Some(Target::Invalid { reason, .. }) if reason.contains("not found"))
        );
    }
    #[test]
    fn diff_each_change_and_position_only_keep() {
        let d = display("a");
        let current = BTreeMap::from([(
            d.id.clone(),
            Running {
                target: play(),
                frame: d.frame,
                scale: d.scale,
            },
        )]);
        let targets = BTreeMap::from([(d.id.clone(), play())]);
        for change in 0..9 {
            let mut next = d.clone();
            let mut targets = targets.clone();
            match change {
                1 => next.frame.x = -1920,
                2 => next.frame.y = 1080,
                3 => next.frame.width = 1280,
                4 => next.frame.height = 720,
                5 => next.scale = 2.0,
                6 => {
                    targets.insert(
                        d.id.clone(),
                        Target::Invalid {
                            item: "video".into(),
                            reason: "changed".into(),
                        },
                    );
                }
                7 => {
                    if let Target::Play { fit, .. } = targets.get_mut(&d.id).unwrap() {
                        *fit = Fit::Fit;
                    }
                }
                8 => {
                    if let Target::Play { kind, .. } = targets.get_mut(&d.id).unwrap() {
                        *kind = Kind::Image;
                    }
                }
                _ => {}
            }
            let result = diff(&current, &[next], &targets);
            if change < 3 {
                assert_eq!(
                    result,
                    Changes {
                        keep: vec![d.id.clone()],
                        ..Changes::default()
                    }
                );
            } else {
                assert_eq!(
                    result,
                    Changes {
                        stop: vec![d.id.clone()],
                        start: vec![d.id.clone()],
                        keep: vec![]
                    }
                );
            }
        }
        assert_eq!(diff(&current, &[], &targets).stop, vec![d.id.clone()]);
        assert_eq!(
            diff(&current, std::slice::from_ref(&d), &BTreeMap::new()).stop,
            vec![d.id.clone()]
        );
        assert_eq!(
            diff(&BTreeMap::new(), std::slice::from_ref(&d), &targets).start,
            vec![d.id]
        );
    }
    #[test]
    fn assignment_and_fit_changes_restart_only_affected_screen() {
        let a = display("a");
        let b = display("b");
        let current = [a.clone(), b.clone()]
            .into_iter()
            .map(|d| {
                (
                    d.id,
                    Running {
                        target: play(),
                        frame: d.frame,
                        scale: d.scale,
                    },
                )
            })
            .collect();
        for fit_only in [false, true] {
            let mut changed = play();
            if let Target::Play { item, fit, .. } = &mut changed {
                if fit_only {
                    *fit = Fit::Stretch;
                } else {
                    *item = "other".into();
                }
            }
            let targets = BTreeMap::from([(a.id.clone(), changed), (b.id.clone(), play())]);
            let changes = diff(&current, &[a.clone(), b.clone()], &targets);
            assert_eq!(changes.stop, vec![a.id.clone()]);
            assert_eq!(changes.start, vec![a.id.clone()]);
            assert_eq!(changes.keep, vec![b.id.clone()]);
        }
    }
    #[test]
    fn other_screen_added_or_removed_keeps_existing_screen() {
        let a = display("a");
        let b = display("b");
        let mut current = BTreeMap::from([(
            a.id.clone(),
            Running {
                target: play(),
                frame: a.frame,
                scale: a.scale,
            },
        )]);
        let targets = BTreeMap::from([(a.id.clone(), play()), (b.id.clone(), play())]);
        let result = diff(&current, &[a.clone(), b.clone()], &targets);
        assert_eq!(result.keep, vec![a.id.clone()]);
        assert_eq!(result.start, vec![b.id.clone()]);
        current.insert(
            b.id.clone(),
            Running {
                target: play(),
                frame: b.frame,
                scale: b.scale,
            },
        );
        let result = diff(&current, std::slice::from_ref(&a), &targets);
        assert_eq!(result.keep, vec![a.id]);
        assert_eq!(result.stop, vec![b.id]);
    }
    #[test]
    fn layout_is_stable_and_dedupe_matches_go_suffixes() {
        let mut ds = vec![display("same"); 4];
        ds[0].frame.x = 10;
        ds[1].frame.y = -10;
        ds[2].name = "first at origin".into();
        ds[3].name = "second at origin".into();
        sort_by_layout(&mut ds);
        dedupe(&mut ds);
        assert_eq!(
            ds.iter().map(|d| d.id.0.as_str()).collect::<Vec<_>>(),
            ["same", "same#2", "same#3", "same#4"]
        );
        assert_eq!(ds[0].frame.y, -10);
        assert_eq!(ds[1].name, "first at origin");
        assert_eq!(ds[2].name, "second at origin");
    }
    #[test]
    fn gate_empty_jitter_two_observations_and_return_to_applied() {
        let a = vec![display("a")];
        let b = vec![display("b")];
        let mut gate = TopologyGate::new(a.clone());
        assert_eq!(gate.observe(vec![]), None);
        assert_eq!(gate.observe(b.clone()), None);
        assert_eq!(gate.observe(a.clone()), None);
        assert_eq!(gate.observe(b.clone()), None);
        assert_eq!(gate.observe(vec![]), None); // Does not clear pending observation.
        assert_eq!(gate.observe(b.clone()), Some(b.clone()));
        assert_eq!(gate.observe(b), None);
        assert_eq!(gate.observe(a.clone()), None);
        assert_eq!(gate.observe(a.clone()), Some(a));
    }
}
