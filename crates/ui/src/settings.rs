use gpui::{App, Context, IntoElement, Render, Window, div, prelude::*, rgb};
use std::rc::Rc;
use wallpaper_core::{Command, Snapshot};

pub struct Settings {
    source: Rc<dyn Fn() -> Snapshot>,
    #[allow(clippy::type_complexity)]
    dispatch: Rc<dyn Fn(Command, &mut App)>,
}

impl Settings {
    #[allow(clippy::type_complexity)]
    pub fn new(source: Rc<dyn Fn() -> Snapshot>, dispatch: Rc<dyn Fn(Command, &mut App)>) -> Self {
        Self { source, dispatch }
    }
}

impl Render for Settings {
    fn render(&mut self, _window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        let snapshot = (self.source)();
        let phase = snapshot.phase.as_str();
        div()
            .size_full()
            .flex()
            .flex_col()
            .gap_3()
            .p_5()
            .bg(rgb(0x20252b))
            .text_color(rgb(0xf0f0f0))
            .child("Wallpaper · Rust + GPUI spike")
            .child(format!("Phase: {phase}"))
            .child(format!("Reason: {}", snapshot.reason))
            .child(format!(
                "位置: {} 秒",
                snapshot
                    .position
                    .map(|p| format!("{p:.2}"))
                    .unwrap_or_else(|| "unknown".into())
            ))
            .child(format!(
                "循环次数: {}",
                snapshot
                    .loops
                    .map(|n| n.to_string())
                    .unwrap_or_else(|| "unknown".into())
            ))
            .child(format!("视频: {}", snapshot.video))
            .child(
                div()
                    .id("pause")
                    .p_2()
                    .bg(rgb(0x365d80))
                    .cursor_pointer()
                    .child(if snapshot.paused { "继续" } else { "暂停" })
                    .on_click(cx.listener(|this, _, _, cx| {
                        (this.dispatch)(Command::TogglePause, cx);
                        cx.notify();
                    })),
            )
    }
}
