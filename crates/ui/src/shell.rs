use crate::{displays, icons::icon, library, settings, style::*};
use gpui::{prelude::*, *};
use std::{ops::Range, path::PathBuf, rc::Rc};
use wallpaper_core::{
    Phase, Snapshot,
    config::{BatteryPolicy, Settings},
    displays::{Assignments, DisplayId, DisplayInfo},
    library::{Fit, Item},
};
#[derive(Clone, Debug)]
pub enum Setting {
    Battery(BatteryPolicy),
    Threshold(u8),
    Fullscreen(bool),
    Covered(bool),
    Launch(bool),
    OpenWindow(bool),
}
#[derive(Clone, Debug)]
pub enum UiCommand {
    TogglePause,
    Import(Vec<PathBuf>),
    AssignAll(String),
    AssignDisplay(String, DisplayId),
    Fit(String, Fit),
    PreviewDelete(String),
    Delete(String),
    Setting(Setting),
    Error(String),
    DismissError,
    CancelDelete,
}
#[derive(Clone)]
pub struct UiItem {
    pub item: Item,
    pub thumbnail: Option<PathBuf>,
}
#[derive(Clone)]
pub struct DeleteConfirmation {
    pub id: String,
    pub description: String,
    pub blocked: bool,
}
#[derive(Clone)]
pub struct UiSnapshot {
    pub playback: Rc<Snapshot>,
    pub items: Vec<UiItem>,
    pub displays: Vec<DisplayInfo>,
    pub assignments: Assignments,
    pub settings: Settings,
    pub busy: bool,
    pub progress: String,
    pub error: Option<String>,
    pub notice: String,
    pub persistent_disabled: Option<String>,
    pub deletion: Option<DeleteConfirmation>,
    pub display_errors: Vec<String>,
}
impl UiSnapshot {
    /// The library item a display shows: its own assignment, else the default.
    pub(crate) fn assigned(&self, display: &DisplayInfo) -> Option<&UiItem> {
        let own = self.assignments.displays.get(&display.id).or_else(|| {
            display
                .legacy_ids
                .iter()
                .find_map(|id| self.assignments.displays.get(id))
        });
        let id = own
            .and_then(|a| a.item.as_ref())
            .or(self.assignments.default.as_ref())?;
        self.items.iter().find(|i| &i.item.id == id)
    }
}
pub type Source = Rc<dyn Fn() -> UiSnapshot>;
pub type Dispatch = Rc<dyn Fn(UiCommand, &mut App)>;
/// What a dropdown option does when chosen.
pub(crate) type Act = Rc<dyn Fn(&mut Shell, &mut App)>;
pub(crate) fn send(command: UiCommand) -> Act {
    Rc::new(move |shell, cx| (shell.dispatch)(command.clone(), cx))
}
#[derive(Clone, Copy, PartialEq, Eq)]
pub(crate) enum Overlay {
    None,
    Displays,
    Settings,
}
#[derive(Clone, Copy, PartialEq, Eq)]
pub(crate) enum Sort {
    Name,
    Added,
    Kind,
    Size,
}
pub struct Shell {
    pub(crate) source: Source,
    pub(crate) dispatch: Dispatch,
    pub(crate) selected: Option<String>,
    pub(crate) display: Option<DisplayId>,
    pub(crate) overlay: Overlay,
    pub(crate) settings_tab: usize,
    pub(crate) menu: Option<&'static str>,
    pub(crate) query: String,
    pub(crate) marked: Option<Range<usize>>,
    pub(crate) sort: Sort,
    pub(crate) descending: bool,
    pub(crate) focus: FocusHandle,
    root: FocusHandle,
}
impl Shell {
    pub fn new(
        source: Source,
        dispatch: Dispatch,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) -> Self {
        // Key events only reach the root once something inside the window has focus.
        let root = cx.focus_handle();
        window.focus(&root, cx);
        Self {
            source,
            dispatch,
            selected: None,
            display: None,
            overlay: Overlay::None,
            settings_tab: 0,
            menu: None,
            query: String::new(),
            marked: None,
            sort: Sort::Name,
            descending: false,
            focus: cx.focus_handle(),
            root,
        }
    }
    /// The display that header, grid marks and "apply" target, with its 1-based number.
    pub(crate) fn target<'a>(&self, s: &'a UiSnapshot) -> Option<(usize, &'a DisplayInfo)> {
        s.displays
            .iter()
            .enumerate()
            .find(|(_, d)| Some(&d.id) == self.display.as_ref())
            .map(|(i, d)| (i + 1, d))
    }
    pub(crate) fn action(
        &self,
        id: impl Into<ElementId>,
        glyph: Option<&'static str>,
        label: impl Into<SharedString>,
        command: UiCommand,
        enabled: bool,
        kind: Button,
    ) -> Stateful<Div> {
        let dispatch = self.dispatch.clone();
        button(id, glyph, label, enabled, kind).on_click(move |_, _, cx| {
            if enabled {
                dispatch(command.clone(), cx);
            }
        })
    }
    pub(crate) fn import_button(
        &self,
        id: &'static str,
        kind: Button,
        busy: bool,
    ) -> Stateful<Div> {
        let dispatch = self.dispatch.clone();
        button(id, Some("folder"), "打开壁纸", !busy, kind).on_click(move |_, _, cx| {
            if busy {
                return;
            }
            let receiver = cx.prompt_for_paths(PathPromptOptions {
                files: true,
                directories: false,
                multiple: true,
                prompt: Some("导入壁纸".into()),
            });
            let dispatch = dispatch.clone();
            cx.spawn(async move |cx| {
                let command = match receiver.await {
                    Ok(Ok(Some(paths))) if !paths.is_empty() => Some(UiCommand::Import(paths)),
                    Ok(Ok(_)) => None,
                    Ok(Err(e)) => Some(UiCommand::Error(format!("文件选择失败：{e}"))),
                    Err(e) => Some(UiCommand::Error(format!("文件选择通道失败：{e}"))),
                };
                if let Some(command) = command {
                    cx.update(|cx| dispatch(command, cx));
                }
            })
            .detach();
        })
    }
    /// Sooty-style select: a bordered field whose options open in a deferred layer.
    pub(crate) fn dropdown(
        &self,
        key: &'static str,
        width: f32,
        options: Vec<(SharedString, bool, Act)>,
        enabled: bool,
        cx: &mut Context<Self>,
    ) -> Stateful<Div> {
        let open = self.menu == Some(key);
        let current = options
            .iter()
            .find(|o| o.1)
            .map(|o| o.0.clone())
            .unwrap_or_default();
        column()
            .id(key)
            .relative()
            .w(px(width))
            .flex_shrink_0()
            .child(
                row()
                    .h(px(CONTROL))
                    .px(px(8.))
                    .justify_between()
                    .border_1()
                    .border_color(rgb(if open { QUIET } else { BORDER }))
                    .bg(rgb(CARD))
                    .child(current)
                    .child(icon("chevron", 14., MUTED)),
            )
            .when(!enabled, |s| s.opacity(0.4))
            .when(enabled, |s| {
                s.cursor_pointer()
                    .on_click(cx.listener(move |this, _, _, cx| {
                        this.menu = (!open).then_some(key);
                        cx.notify();
                    }))
            })
            .when(open, |s| {
                s.child(
                    deferred(
                        column()
                            .id((key, 1000usize))
                            .occlude()
                            .absolute()
                            .top(px(CONTROL + 2.))
                            .right_0()
                            .w_full()
                            .py(px(4.))
                            .bg(rgb(PANEL))
                            .border_1()
                            .border_color(rgb(BORDER))
                            .shadow_lg()
                            .children(options.into_iter().enumerate().map(
                                |(index, (label, selected, act))| {
                                    row()
                                        .id((key, index))
                                        .h(px(CONTROL))
                                        .px(px(8.))
                                        .gap(px(6.))
                                        .cursor_pointer()
                                        .hover(|s| s.bg(rgb(SELECTED)))
                                        .child(div().w(px(12.)).child(if selected {
                                            "✓"
                                        } else {
                                            ""
                                        }))
                                        .child(label)
                                        .on_click(cx.listener(move |this, _, _, cx| {
                                            cx.stop_propagation();
                                            this.menu = None;
                                            act(this, cx);
                                            cx.notify();
                                        }))
                                },
                            )),
                    )
                    .with_priority(3),
                )
            })
    }
    fn header(&self, s: &UiSnapshot, cx: &mut Context<Self>) -> Div {
        let target = self.target(s);
        row()
            .px(px(16.))
            .pt(px(14.))
            .gap(px(12.))
            .flex_shrink_0()
            .child(
                div()
                    .text_size(px(26.))
                    .line_height(px(34.))
                    .child(match target {
                        Some((number, _)) => format!("显示器 {number}"),
                        None => "未检测到显示器".into(),
                    }),
            )
            .child(
                button("choose-display", None, "选择显示器", true, Button::Outline).on_click(
                    cx.listener(|this, _, _, cx| {
                        this.overlay = Overlay::Displays;
                        this.menu = None;
                        cx.notify();
                    }),
                ),
            )
            .when_some(target, |v, (_, display)| {
                v.child(
                    div()
                        .flex_1()
                        .min_w_0()
                        .truncate()
                        .text_color(rgb(MUTED))
                        .child(format!(
                            "{} · {} × {}",
                            display.name, display.frame.width, display.frame.height
                        )),
                )
            })
    }
    fn tabs(&self, s: &UiSnapshot, cx: &mut Context<Self>) -> Div {
        let can_pause = s.playback.paused
            || s.persistent_disabled.is_some()
            || s.displays.iter().any(|d| s.assigned(d).is_some());
        column()
            .px(px(16.))
            .pt(px(10.))
            .flex_shrink_0()
            .child(
                row()
                    .gap(px(6.))
                    .child(
                        row()
                            .h(px(CONTROL))
                            .px(px(10.))
                            .gap(px(6.))
                            .bg(rgb(TEXT))
                            .text_color(rgb(BACKGROUND))
                            .child(icon("image", 14., BACKGROUND))
                            .child("壁纸库")
                            .child(
                                div()
                                    .text_size(px(12.))
                                    .text_color(rgb(QUIET))
                                    .child(s.items.len().to_string()),
                            ),
                    )
                    .child(div().flex_1())
                    .child(self.action(
                        "pause",
                        Some(if s.playback.paused { "play" } else { "pause" }),
                        if s.playback.paused {
                            "继续"
                        } else {
                            "暂停"
                        },
                        UiCommand::TogglePause,
                        can_pause,
                        Button::Outline,
                    ))
                    .child(
                        button("displays", Some("monitor"), "显示", true, Button::Outline)
                            .on_click(cx.listener(|this, _, _, cx| {
                                this.overlay = Overlay::Displays;
                                this.menu = None;
                                cx.notify();
                            })),
                    )
                    .child(
                        button("settings", Some("gear"), "设置", true, Button::Outline).on_click(
                            cx.listener(|this, _, _, cx| {
                                this.overlay = Overlay::Settings;
                                this.menu = None;
                                cx.notify();
                            }),
                        ),
                    ),
            )
            .child(div().h(px(1.)).w_full().bg(rgb(ACCENT)))
    }
    fn footer(&self, s: &UiSnapshot) -> Div {
        row()
            .h(px(52.))
            .px(px(16.))
            .gap(px(12.))
            .flex_shrink_0()
            .border_t_1()
            .border_color(rgb(BORDER))
            .child(self.import_button("import", Button::Primary, s.busy))
            .child(
                div()
                    .flex_1()
                    .min_w_0()
                    .truncate()
                    .text_size(px(12.))
                    .text_color(rgb(MUTED))
                    .child(format!("主显示器：{}", status_line(&s.playback))),
            )
            .child(
                div()
                    .max_w(px(360.))
                    .truncate()
                    .text_size(px(12.))
                    .text_color(rgb(MUTED))
                    .child(if s.busy {
                        s.progress.clone()
                    } else {
                        s.notice.clone()
                    }),
            )
    }
    fn deletion(&self, s: &UiSnapshot, deletion: DeleteConfirmation) -> Stateful<Div> {
        backdrop("delete-backdrop").child(
            dialog("delete-dialog", 420.)
                .overflow_y_scroll()
                .p(px(16.))
                .gap(px(12.))
                .child(
                    div()
                        .text_size(px(18.))
                        .font_weight(FontWeight::SEMIBOLD)
                        .child("删除壁纸"),
                )
                .child(deletion.description)
                .when(deletion.blocked, |v| {
                    v.child(
                        div()
                            .text_color(rgb(MUTED))
                            .child("请先切换壁纸，再删除此项。"),
                    )
                })
                .child(
                    row()
                        .justify_end()
                        .gap(px(8.))
                        .child(self.action(
                            "cancel-delete",
                            None,
                            "取消",
                            UiCommand::CancelDelete,
                            true,
                            Button::Outline,
                        ))
                        .child(self.action(
                            "confirm-delete",
                            Some("trash"),
                            "删除",
                            UiCommand::Delete(deletion.id),
                            !deletion.blocked && !s.busy,
                            Button::Danger,
                        )),
                ),
        )
    }
}
impl Render for Shell {
    fn render(&mut self, window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        let s = (self.source)();
        if self
            .selected
            .as_ref()
            .is_none_or(|id| !s.items.iter().any(|i| &i.item.id == id))
        {
            self.selected = s.items.first().map(|i| i.item.id.clone());
        }
        if self.target(&s).is_none() {
            self.display = s
                .displays
                .iter()
                .find(|d| d.primary)
                .or(s.displays.first())
                .map(|d| d.id.clone());
        }
        let mut main = column()
            .flex_1()
            .min_w_0()
            .h_full()
            .child(self.header(&s, cx))
            .child(self.tabs(&s, cx));
        if let Some(error) = &s.error {
            main = main.child(notice_bar(error.clone(), DANGER).child(self.action(
                "dismiss",
                Some("close"),
                "关闭",
                UiCommand::DismissError,
                true,
                Button::Text,
            )));
        }
        for error in &s.display_errors {
            main = main.child(notice_bar(error.clone(), DANGER));
        }
        if let Some(reason) = &s.persistent_disabled {
            main = main.child(notice_bar(reason.clone(), MUTED));
        }
        main = main
            .child(library::content(self, &s, window, cx))
            .child(self.footer(&s));
        row()
            .id("root")
            .track_focus(&self.root)
            .on_key_down(cx.listener(|this, event: &KeyDownEvent, _, cx| {
                if event.keystroke.key != "escape" {
                    return;
                }
                if this.menu.take().is_none() {
                    if (this.source)().deletion.is_some() {
                        (this.dispatch)(UiCommand::CancelDelete, cx);
                    } else {
                        this.overlay = Overlay::None;
                    }
                }
                cx.notify();
            }))
            .relative()
            .size_full()
            .items_start()
            .font_family(".SystemUIFont")
            .text_size(px(14.))
            .line_height(px(22.))
            .bg(rgb(BACKGROUND))
            .text_color(rgb(TEXT))
            .child(main)
            .child(library::panel(self, &s, cx))
            .when(self.overlay == Overlay::Displays, |v| {
                v.child(displays::render(self, &s, cx))
            })
            .when(self.overlay == Overlay::Settings, |v| {
                v.child(settings::render(self, &s, cx))
            })
            .when_some(s.deletion.clone(), |v, deletion| {
                v.child(self.deletion(&s, deletion))
            })
            .when(self.menu.is_some(), |v| {
                v.child(
                    deferred(
                        div()
                            .id("menu-dismiss")
                            .occlude()
                            .absolute()
                            .inset_0()
                            .on_mouse_down(
                                MouseButton::Left,
                                cx.listener(|this, _, _, cx| {
                                    this.menu = None;
                                    cx.notify();
                                }),
                            ),
                    )
                    .with_priority(2),
                )
            })
    }
}
fn notice_bar(message: String, color: u32) -> Div {
    row()
        .min_h(px(32.))
        .mx(px(16.))
        .mt(px(8.))
        .px(px(10.))
        .py(px(5.))
        .gap(px(8.))
        .flex_shrink_0()
        .bg(rgb(CARD))
        .border_1()
        .border_color(rgb(BORDER))
        .text_size(px(12.))
        .line_height(px(18.))
        .text_color(rgb(color))
        .child(div().flex_1().min_w_0().child(message))
}
/// Reasons are only meaningful to the user when nothing is playing.
fn status_line(playback: &Snapshot) -> String {
    let reason = playback.reason.as_str();
    if playback.paused {
        return "已暂停".into();
    }
    match playback.phase {
        Phase::Starting => "启动中".into(),
        Phase::Confirmed => "播放中".into(),
        Phase::Stopped if !reason.is_empty() => reason.into(),
        Phase::Stopped => "已停止".into(),
        Phase::Failed if !reason.is_empty() => format!("失败 · {reason}"),
        Phase::Failed => "失败".into(),
    }
}
#[cfg(test)]
mod tests {
    use super::{Phase, Snapshot, status_line};
    fn snapshot(phase: Phase, reason: &str, paused: bool) -> Snapshot {
        Snapshot {
            phase,
            reason: reason.into(),
            paused,
            ..Snapshot::idle()
        }
    }
    #[test]
    fn status_line_shows_reasons_only_when_not_playing() {
        assert_eq!(
            status_line(&snapshot(Phase::Confirmed, "", false)),
            "播放中"
        );
        assert_eq!(
            status_line(&snapshot(Phase::Stopped, "未分配壁纸", false)),
            "未分配壁纸"
        );
        assert_eq!(
            status_line(&snapshot(Phase::Failed, "decoder failed", false)),
            "失败 · decoder failed"
        );
        assert_eq!(status_line(&snapshot(Phase::Failed, "x", true)), "已暂停");
    }
}
