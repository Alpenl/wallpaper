//! Shared visual tokens and controls, kept in the Sooty component style:
//! square corners, one-pixel borders, near-black surfaces and a single accent.
use crate::icons::icon;
use gpui::{prelude::*, *};
pub const BACKGROUND: u32 = 0x0b0b0b;
pub const PANEL: u32 = 0x101010;
pub const CARD: u32 = 0x161616;
pub const SELECTED: u32 = 0x242424;
pub const BORDER: u32 = 0x292929;
pub const TEXT: u32 = 0xe5e5e5;
pub const MUTED: u32 = 0x898989;
pub const QUIET: u32 = 0x656565;
pub const ACCENT: u32 = 0x79b8dc;
pub const DANGER: u32 = 0xd78c86;
pub const INSPECTOR: f32 = 300.;
pub const TILE: f32 = 164.;
pub const CONTROL: f32 = 28.;
pub fn row() -> Div {
    div().flex().items_center()
}
pub fn column() -> Div {
    div().flex().flex_col()
}
#[derive(Clone, Copy)]
pub enum Button {
    Outline,
    Primary,
    Danger,
    Text,
}
pub fn button(
    id: impl Into<ElementId>,
    glyph: Option<&'static str>,
    label: impl Into<SharedString>,
    enabled: bool,
    kind: Button,
) -> Stateful<Div> {
    let ink = match kind {
        Button::Primary => BACKGROUND,
        Button::Danger => DANGER,
        _ => TEXT,
    };
    row()
        .id(id)
        .h(px(CONTROL))
        .px(px(10.))
        .gap(px(6.))
        .justify_center()
        .flex_shrink_0()
        .text_color(rgb(ink))
        .when(matches!(kind, Button::Outline | Button::Danger), |s| {
            s.bg(rgb(CARD)).border_1().border_color(rgb(BORDER))
        })
        .when(matches!(kind, Button::Primary), |s| s.bg(rgb(TEXT)))
        .when(matches!(kind, Button::Text), |s| s.px(px(6.)))
        .when(enabled, |s| {
            s.cursor_pointer().hover(move |s| match kind {
                Button::Primary => s.bg(rgb(0xc9c9c9)),
                Button::Danger => s.bg(rgb(SELECTED)).border_color(rgb(DANGER)),
                _ => s.bg(rgb(SELECTED)).border_color(rgb(QUIET)),
            })
        })
        .when(!enabled, |s| s.opacity(0.4))
        .when_some(glyph, |s, glyph| s.child(icon(glyph, 14., ink)))
        .child(label.into())
}
pub fn switch(on: bool) -> Div {
    row()
        .w(px(36.))
        .h(px(20.))
        .p(px(2.))
        .flex_shrink_0()
        .bg(rgb(if on { TEXT } else { BORDER }))
        .when(on, |s| s.justify_end())
        .child(div().size(px(16.)).bg(rgb(BACKGROUND)))
}
pub fn chip(label: impl Into<SharedString>) -> Div {
    row()
        .h(px(22.))
        .px(px(8.))
        .bg(rgb(CARD))
        .border_1()
        .border_color(rgb(BORDER))
        .text_size(px(12.))
        .text_color(rgb(MUTED))
        .child(label.into())
}
/// Section title followed by a rule, as used above grouped properties.
pub fn section(glyph: Option<&'static str>, label: &'static str) -> Div {
    row()
        .gap(px(8.))
        .when_some(glyph, |s, glyph| s.child(icon(glyph, 14., MUTED)))
        .child(div().font_weight(FontWeight::SEMIBOLD).child(label))
        .child(div().h(px(1.)).flex_1().bg(rgb(BORDER)))
}
/// Dimmed full-window layer that hosts one centered or top-anchored dialog.
pub fn backdrop(id: &'static str) -> Stateful<Div> {
    row()
        .id(id)
        .occlude()
        .absolute()
        .inset_0()
        .justify_center()
        .bg(rgba(0x00000088))
        .on_mouse_down(MouseButton::Left, |_, _, cx| cx.stop_propagation())
}
pub fn dialog(id: &'static str, width: f32) -> Stateful<Div> {
    column()
        .id(id)
        .occlude()
        .w(px(width))
        .max_h_full()
        .bg(rgb(BACKGROUND))
        .border_1()
        .border_color(rgb(BORDER))
        .shadow_2xl()
}
