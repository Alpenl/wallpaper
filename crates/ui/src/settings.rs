//! Settings dialog: tabs across the top, bordered groups of "label: control" rows.
//! Every change is saved as soon as it is made, so the dialog only needs "完成".
use crate::{
    icons::icon,
    shell::{Act, Overlay, Setting, Shell, UiCommand, UiSnapshot, send},
    style::*,
};
use gpui::{prelude::*, *};
use wallpaper_core::config::BatteryPolicy;

const TABS: [&str; 3] = ["性能", "常规", "关于"];
const FIELD: f32 = 220.;

fn group(glyph: &'static str, title: &'static str) -> Div {
    column()
        .mt(px(16.))
        .px(px(14.))
        .pb(px(8.))
        .border_1()
        .border_color(rgb(BORDER))
        .child(
            row()
                .h(px(36.))
                .gap(px(8.))
                .child(icon(glyph, 14., TEXT))
                .child(div().font_weight(FontWeight::SEMIBOLD).child(title)),
        )
}
fn field(label: &'static str, control: impl IntoElement) -> Div {
    row()
        .min_h(px(40.))
        .gap(px(12.))
        .child(
            div()
                .flex_1()
                .text_color(rgb(MUTED))
                .child(format!("{label}：")),
        )
        .child(row().w(px(FIELD)).flex_shrink_0().child(control))
}
fn note(text: &'static str) -> Div {
    div()
        .pb(px(6.))
        .text_size(px(12.))
        .text_color(rgb(QUIET))
        .child(text)
}
/// Two-way "pause or keep running" choice shared by the playback rules.
fn rule(pauses: bool, pause: Setting, keep: Setting) -> Vec<(SharedString, bool, Act)> {
    vec![
        ("暂停".into(), pauses, send(UiCommand::Setting(pause))),
        ("保持运行".into(), !pauses, send(UiCommand::Setting(keep))),
    ]
}
fn toggle(
    shell: &Shell,
    id: &'static str,
    on: bool,
    setting: Setting,
    enabled: bool,
) -> Stateful<Div> {
    let dispatch = shell.dispatch.clone();
    div()
        .id(id)
        .when(enabled, |v| v.cursor_pointer())
        .when(!enabled, |v| v.opacity(0.4))
        .child(switch(on))
        .on_click(move |_, _, cx| {
            if enabled {
                dispatch(UiCommand::Setting(setting.clone()), cx);
            }
        })
}
fn threshold(shell: &Shell, value: u8, enabled: bool) -> Div {
    let step_button = |id: &'static str, label: &'static str, next: u8| {
        let dispatch = shell.dispatch.clone();
        row()
            .id(id)
            .size(px(20.))
            .justify_center()
            .text_color(rgb(MUTED))
            .when(enabled, |v| {
                v.cursor_pointer().hover(|v| v.bg(rgb(SELECTED)))
            })
            .child(label)
            .on_click(move |_, _, cx| {
                if enabled {
                    dispatch(UiCommand::Setting(Setting::Threshold(next)), cx);
                }
            })
    };
    let knob = (value as usize + 2) / 5;
    row()
        .w_full()
        .gap(px(6.))
        .when(!enabled, |v| v.opacity(0.4))
        .child(step_button("threshold-less", "−", value.saturating_sub(1)))
        .child(
            row()
                .flex_1()
                .h(px(18.))
                .children((0..=20usize).map(|step| {
                    let dispatch = shell.dispatch.clone();
                    row()
                        .id(("threshold-step", step))
                        .flex_1()
                        .h_full()
                        .when(enabled, |v| v.cursor_pointer())
                        .child(
                            div()
                                .relative()
                                .h(px(1.))
                                .w_full()
                                .bg(rgb(if step <= knob { TEXT } else { BORDER }))
                                .when(step == knob, |v| {
                                    v.child(
                                        div().absolute().top(px(-3.)).size(px(7.)).bg(rgb(TEXT)),
                                    )
                                }),
                        )
                        .on_click(move |_, _, cx| {
                            if enabled {
                                dispatch(
                                    UiCommand::Setting(Setting::Threshold(step as u8 * 5)),
                                    cx,
                                );
                            }
                        })
                })),
        )
        .child(step_button(
            "threshold-more",
            "+",
            value.saturating_add(1).min(100),
        ))
        .child(div().w(px(40.)).text_right().child(format!("{value}%")))
}
fn performance(shell: &Shell, s: &UiSnapshot, cx: &mut Context<Shell>) -> Div {
    let pause = &s.settings.pause;
    let enabled = !s.busy;
    let battery = pause.battery_policy == BatteryPolicy::Pause;
    group("play", "回放")
        .child(field(
            "使用电池供电时",
            shell.dropdown(
                "battery",
                FIELD,
                rule(
                    battery,
                    Setting::Battery(BatteryPolicy::Pause),
                    Setting::Battery(BatteryPolicy::Ignore),
                ),
                enabled,
                cx,
            ),
        ))
        .child(field(
            "低电量阈值",
            threshold(shell, pause.low_battery_pct, enabled),
        ))
        .child(note("电量不高于阈值时始终暂停，与上面的电池供电选项无关。"))
        .child(field(
            "其他应用程序全屏时",
            shell.dropdown(
                "fullscreen",
                FIELD,
                rule(
                    pause.on_fullscreen,
                    Setting::Fullscreen(true),
                    Setting::Fullscreen(false),
                ),
                enabled,
                cx,
            ),
        ))
        .child(field(
            "壁纸被完全遮挡时",
            shell.dropdown(
                "covered",
                FIELD,
                rule(
                    pause.on_covered,
                    Setting::Covered(true),
                    Setting::Covered(false),
                ),
                enabled,
                cx,
            ),
        ))
}
fn general(shell: &Shell, s: &UiSnapshot) -> Div {
    let startup = &s.settings.startup;
    let enabled = !s.busy;
    group("power", "自动启动")
        .child(field(
            "开机启动",
            toggle(
                shell,
                "launch",
                startup.launch_at_login,
                Setting::Launch(!startup.launch_at_login),
                enabled,
            ),
        ))
        .child(field(
            "启动时打开此窗口",
            toggle(
                shell,
                "open-window",
                startup.open_settings,
                Setting::OpenWindow(!startup.open_settings),
                enabled,
            ),
        ))
        .child(note("“启动时打开此窗口”在下次启动时生效。"))
}
fn about() -> Div {
    group("info", "关于")
        .child(field("名称", "Wallpaper"))
        .child(field("版本", env!("CARGO_PKG_VERSION")))
        .child(field("界面", "Rust + GPUI"))
}
pub fn render(shell: &Shell, s: &UiSnapshot, cx: &mut Context<Shell>) -> Stateful<Div> {
    let close = |this: &mut Shell, _: &ClickEvent, _: &mut Window, cx: &mut Context<Shell>| {
        this.overlay = Overlay::None;
        this.menu = None;
        cx.notify();
    };
    let page = match shell.settings_tab {
        0 => performance(shell, s, cx),
        1 => general(shell, s),
        _ => about(),
    };
    backdrop("settings-backdrop").child(
        dialog("settings-dialog", 560.)
            .h(px(520.))
            .child(
                row()
                    .h(px(44.))
                    .px(px(12.))
                    .flex_shrink_0()
                    .child(div().w(px(22.)))
                    .child(
                        div()
                            .flex_1()
                            .text_center()
                            .font_weight(FontWeight::SEMIBOLD)
                            .child("设置"),
                    )
                    .child(
                        row()
                            .id("settings-close")
                            .size(px(22.))
                            .justify_center()
                            .cursor_pointer()
                            .hover(|v| v.bg(rgb(SELECTED)))
                            .child(icon("close", 14., TEXT))
                            .on_click(cx.listener(close)),
                    ),
            )
            .child(
                column()
                    .px(px(16.))
                    .flex_shrink_0()
                    .child(
                        row().children(TABS.into_iter().enumerate().map(|(index, label)| {
                            let selected = shell.settings_tab == index;
                            row()
                                .id(("settings-tab", index))
                                .h(px(CONTROL))
                                .px(px(12.))
                                .cursor_pointer()
                                .when(selected, |v| v.bg(rgb(TEXT)).text_color(rgb(BACKGROUND)))
                                .when(!selected, |v| {
                                    v.text_color(rgb(MUTED)).hover(|v| v.bg(rgb(SELECTED)))
                                })
                                .child(label)
                                .on_click(cx.listener(move |this, _, _, cx| {
                                    this.settings_tab = index;
                                    this.menu = None;
                                    cx.notify();
                                }))
                        })),
                    )
                    .child(div().h(px(1.)).w_full().bg(rgb(ACCENT))),
            )
            .child(
                column()
                    .id(("settings-scroll", shell.settings_tab))
                    .flex_1()
                    .min_h_0()
                    .overflow_y_scroll()
                    .px(px(16.))
                    .pb(px(16.))
                    .child(page),
            )
            .child(
                row()
                    .p(px(12.))
                    .justify_end()
                    .flex_shrink_0()
                    .border_t_1()
                    .border_color(rgb(BORDER))
                    .child(
                        button("settings-done", None, "完成", true, Button::Primary)
                            .w(px(120.))
                            .on_click(cx.listener(close)),
                    ),
            ),
    )
}
