//! "Choose display" sheet: drops from the top edge and shows every connected display
//! at its real position and proportion, each with the wallpaper it currently shows.
use crate::{
    icons::icon,
    shell::{Overlay, Shell, UiCommand, UiSnapshot},
    style::*,
};
use gpui::{prelude::*, *};

const STAGE_WIDTH: f32 = 560.;
const STAGE_HEIGHT: f32 = 230.;

pub fn render(shell: &Shell, s: &UiSnapshot, cx: &mut Context<Shell>) -> Stateful<Div> {
    let enabled = !s.busy && s.persistent_disabled.is_none();
    let left = s.displays.iter().map(|d| d.frame.x).min().unwrap_or(0);
    let top = s.displays.iter().map(|d| d.frame.y).min().unwrap_or(0);
    let right = s
        .displays
        .iter()
        .map(|d| d.frame.x + d.frame.width as i32)
        .max()
        .unwrap_or(1);
    let bottom = s
        .displays
        .iter()
        .map(|d| d.frame.y + d.frame.height as i32)
        .max()
        .unwrap_or(1);
    let (width, height) = ((right - left).max(1) as f32, (bottom - top).max(1) as f32);
    let scale = (STAGE_WIDTH / width).min(STAGE_HEIGHT / height);
    let (shift_x, shift_y) = (
        (STAGE_WIDTH - width * scale) / 2.,
        (STAGE_HEIGHT - height * scale) / 2.,
    );
    let mut stage = div()
        .relative()
        .w(px(STAGE_WIDTH))
        .h(px(STAGE_HEIGHT))
        .flex_shrink_0();
    for (index, display) in s.displays.iter().enumerate() {
        let id = display.id.clone();
        let selected = shell.display.as_ref() == Some(&id);
        let art = match s.assigned(display).and_then(|i| i.thumbnail.clone()) {
            Some(path) => img(path)
                .size_full()
                .object_fit(ObjectFit::Cover)
                .into_any_element(),
            None => row()
                .size_full()
                .justify_center()
                .child(icon("monitor", 28., QUIET))
                .into_any_element(),
        };
        stage = stage.child(
            div()
                .id(("monitor", index))
                .absolute()
                .left(px(shift_x + (display.frame.x - left) as f32 * scale))
                .top(px(shift_y + (display.frame.y - top) as f32 * scale))
                .w(px(display.frame.width as f32 * scale))
                .h(px(display.frame.height as f32 * scale))
                .overflow_hidden()
                .bg(rgb(CARD))
                .border_1()
                .border_color(rgb(if selected { ACCENT } else { BORDER }))
                .cursor_pointer()
                .when(!selected, |v| v.hover(|v| v.border_color(rgb(QUIET))))
                .child(art)
                .child(
                    div()
                        .absolute()
                        .top_0()
                        .left_0()
                        .max_w_full()
                        .px(px(5.))
                        .bg(rgba(0x0b0b0bcc))
                        .text_size(px(11.))
                        .line_height(px(18.))
                        .truncate()
                        .child(format!(
                            "{} · {}×{} · {}",
                            index + 1,
                            display.frame.width,
                            display.frame.height,
                            display.name
                        )),
                )
                .on_click(cx.listener(move |this, _, _, cx| {
                    this.display = Some(id.clone());
                    cx.notify();
                })),
        );
    }
    let target = shell.target(s).map(|(_, d)| d);
    let showing = target.and_then(|d| s.assigned(d));
    let close = |this: &mut Shell, _: &ClickEvent, _: &mut Window, cx: &mut Context<Shell>| {
        this.overlay = Overlay::None;
        cx.notify();
    };
    backdrop("displays-backdrop").items_start().child(
        dialog("displays-dialog", 640.)
            .items_center()
            .p(px(20.))
            .gap(px(16.))
            .border_t_0()
            .child(
                div()
                    .id("displays-collapse")
                    .cursor_pointer()
                    .child(icon("ascending", 18., ACCENT))
                    .on_click(cx.listener(close)),
            )
            .child(
                div()
                    .text_size(px(24.))
                    .line_height(px(30.))
                    .child("选择显示器"),
            )
            .child(if s.displays.is_empty() {
                row()
                    .h(px(STAGE_HEIGHT))
                    .text_color(rgb(MUTED))
                    .child("未检测到显示器")
            } else {
                stage
            })
            .child(
                div()
                    .text_size(px(12.))
                    .text_color(rgb(MUTED))
                    .child(match showing {
                        Some(entry) => format!("当前壁纸：{}", entry.item.title),
                        None => "此显示器尚未设置壁纸".into(),
                    }),
            )
            .child(
                row()
                    .gap(px(8.))
                    .child(
                        button("change-wallpaper", None, "更换壁纸", true, Button::Primary)
                            .w(px(180.))
                            .on_click(cx.listener(close)),
                    )
                    .child(
                        shell
                            .action(
                                "unify-wallpaper",
                                None,
                                "用于全部显示器",
                                UiCommand::AssignAll(
                                    showing.map(|i| i.item.id.clone()).unwrap_or_default(),
                                ),
                                enabled && showing.is_some() && s.displays.len() > 1,
                                Button::Outline,
                            )
                            .w(px(180.)),
                    ),
            ),
    )
}
