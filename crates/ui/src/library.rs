//! Installed-wallpaper browser: filter row and tile grid in the center, with the
//! selected wallpaper's preview and properties in the right panel.
use crate::{
    icons::icon,
    input,
    shell::{Act, Shell, Sort, UiCommand, UiItem, UiSnapshot, send},
    style::*,
};
use gpui::{prelude::*, *};
use std::rc::Rc;
use wallpaper_core::library::{Fit, Item, Kind};

fn visible<'a>(shell: &Shell, s: &'a UiSnapshot) -> Vec<&'a UiItem> {
    let query = shell.query.trim().to_lowercase();
    let mut items: Vec<_> = s
        .items
        .iter()
        .filter(|i| i.item.title.to_lowercase().contains(&query))
        .collect();
    items.sort_by(|a, b| {
        let (a, b) = (&a.item, &b.item);
        let title = || a.title.to_lowercase().cmp(&b.title.to_lowercase());
        match shell.sort {
            Sort::Name => title(),
            Sort::Added => a.added_at.cmp(&b.added_at).then_with(title),
            Sort::Kind => (a.kind == Kind::Image)
                .cmp(&(b.kind == Kind::Image))
                .then_with(title),
            Sort::Size => a.size_bytes.cmp(&b.size_bytes).then_with(title),
        }
    });
    if shell.descending {
        items.reverse();
    }
    items
}
fn sort_option(label: &'static str, sort: Sort, current: Sort) -> (SharedString, bool, Act) {
    (
        label.into(),
        sort == current,
        Rc::new(move |shell, _| shell.sort = sort),
    )
}
fn filters(shell: &Shell, window: &Window, cx: &mut Context<Shell>) -> Div {
    row()
        .px(px(16.))
        .py(px(10.))
        .gap(px(8.))
        .flex_shrink_0()
        .child(input::render(shell, window, cx))
        .child(div().flex_1())
        .child(
            row()
                .id("sort-direction")
                .size(px(CONTROL))
                .justify_center()
                .cursor_pointer()
                .hover(|s| s.bg(rgb(SELECTED)))
                .child(icon(
                    if shell.descending {
                        "chevron"
                    } else {
                        "ascending"
                    },
                    15.,
                    TEXT,
                ))
                .on_click(cx.listener(|this, _, _, cx| {
                    this.descending = !this.descending;
                    cx.notify();
                })),
        )
        .child(shell.dropdown(
            "sort",
            180.,
            vec![
                sort_option("名称", Sort::Name, shell.sort),
                sort_option("添加时间", Sort::Added, shell.sort),
                sort_option("类型", Sort::Kind, shell.sort),
                sort_option("文件大小", Sort::Size, shell.sort),
            ],
            true,
            cx,
        ))
}
fn thumbnail(entry: &UiItem) -> AnyElement {
    match &entry.thumbnail {
        Some(path) => img(path.clone())
            .size_full()
            .object_fit(ObjectFit::Cover)
            .with_loading(|| placeholder("加载中…").into_any_element())
            .with_fallback(|| placeholder("缩略图加载失败").into_any_element())
            .into_any_element(),
        None => placeholder("暂无缩略图").into_any_element(),
    }
}
fn placeholder(label: &'static str) -> Div {
    column()
        .size_full()
        .justify_center()
        .items_center()
        .gap(px(6.))
        .bg(rgb(CARD))
        .text_size(px(12.))
        .text_color(rgb(QUIET))
        .child(icon("image", 28., QUIET))
        .child(label)
}
fn badge(glyph: &'static str, color: u32) -> Div {
    row()
        .absolute()
        .top(px(4.))
        .size(px(20.))
        .justify_center()
        .bg(rgba(0x0b0b0bcc))
        .child(icon(glyph, 11., color))
}
fn tile(
    shell: &Shell,
    index: usize,
    entry: &UiItem,
    in_use: bool,
    apply: Option<UiCommand>,
    cx: &mut Context<Shell>,
) -> Stateful<Div> {
    let id = entry.item.id.clone();
    let selected = shell.selected.as_ref() == Some(&id);
    div()
        .id(("item", index))
        .relative()
        .size(px(TILE))
        .flex_shrink_0()
        .overflow_hidden()
        .bg(rgb(CARD))
        .border_1()
        .border_color(rgb(if selected { ACCENT } else { BORDER }))
        .cursor_pointer()
        .when(!selected, |v| v.hover(|v| v.border_color(rgb(QUIET))))
        .child(thumbnail(entry))
        .child(
            div()
                .absolute()
                .bottom_0()
                .left_0()
                .right_0()
                .px(px(6.))
                .py(px(4.))
                .bg(rgba(0x0b0b0bcc))
                .text_size(px(12.))
                .line_height(px(16.))
                .text_center()
                .text_color(rgb(if selected { TEXT } else { MUTED }))
                .line_clamp(2)
                .child(entry.item.title.clone()),
        )
        .when(entry.item.kind == Kind::Video, |v| {
            v.child(badge("play", TEXT).left(px(4.)))
        })
        .when(in_use, |v| v.child(badge("monitor", ACCENT).right(px(4.))))
        .on_click(cx.listener(move |this, event: &ClickEvent, _, cx| {
            this.selected = Some(id.clone());
            if event.click_count() >= 2
                && let Some(command) = apply.clone()
            {
                (this.dispatch)(command, cx);
            }
            cx.notify();
        }))
}
pub fn content(shell: &Shell, s: &UiSnapshot, window: &Window, cx: &mut Context<Shell>) -> Div {
    let enabled = !s.busy && s.persistent_disabled.is_none();
    let target = shell.target(s).map(|(_, d)| d);
    let showing = target.and_then(|d| s.assigned(d)).map(|i| &i.item.id);
    let items = visible(shell, s);
    let body = column()
        .id("library-scroll")
        .flex_1()
        .min_h_0()
        .overflow_y_scroll()
        .px(px(16.))
        .pb(px(12.));
    let body = if s.items.is_empty() && s.busy {
        body.justify_center()
            .items_center()
            .text_color(rgb(MUTED))
            .child(s.progress.clone())
    } else if s.items.is_empty() {
        body.justify_center()
            .items_center()
            .gap(px(12.))
            .child(
                div()
                    .text_size(px(18.))
                    .font_weight(FontWeight::SEMIBOLD)
                    .child("壁纸库为空"),
            )
            .child(
                div()
                    .text_color(rgb(MUTED))
                    .child("打开图片或视频，把它设置为桌面壁纸。"),
            )
            .child(shell.import_button("empty-import", Button::Primary, s.busy))
    } else if items.is_empty() {
        body.justify_center()
            .items_center()
            .text_color(rgb(MUTED))
            .child("没有匹配的壁纸")
    } else {
        body.child(row().flex_wrap().items_start().gap(px(6.)).children(
            items.into_iter().enumerate().map(|(index, entry)| {
                let apply = target
                    .filter(|_| enabled)
                    .map(|d| UiCommand::AssignDisplay(entry.item.id.clone(), d.id.clone()));
                tile(
                    shell,
                    index,
                    entry,
                    showing == Some(&entry.item.id),
                    apply,
                    cx,
                )
            }),
        ))
    };
    column()
        .flex_1()
        .min_h_0()
        .child(filters(shell, window, cx))
        .child(body)
}
fn property(glyph: &'static str, label: &'static str) -> Div {
    row()
        .h(px(36.))
        .gap(px(10.))
        .child(icon(glyph, 15., MUTED))
        .child(div().flex_1().child(label))
}
fn size_label(bytes: u64) -> String {
    let (unit, divisor) = if bytes >= 1 << 30 {
        ("GB", (1u64 << 30) as f64)
    } else if bytes >= 1 << 20 {
        ("MB", (1u64 << 20) as f64)
    } else {
        ("KB", 1024.)
    };
    format!("{:.1} {unit}", bytes as f64 / divisor)
}
fn duration_label(seconds: f64) -> String {
    let total = seconds.round().max(1.) as u64;
    let (hours, minutes, rest) = (total / 3600, total / 60 % 60, total % 60);
    if hours > 0 {
        format!("{hours}:{minutes:02}:{rest:02}")
    } else {
        format!("{minutes}:{rest:02}")
    }
}
/// Keeps the wallpaper's own proportions; a square preview would crop widescreen art.
fn preview_height(pixels: (Option<u32>, Option<u32>)) -> f32 {
    let width = INSPECTOR - 32.;
    match pixels {
        (Some(w), Some(h)) if w > 0 && h > 0 => (width * h as f32 / w as f32).clamp(120., width),
        _ => width * 9. / 16.,
    }
}
fn summary(item: &Item) -> String {
    let kind = match item.kind {
        Kind::Image => "图片",
        Kind::Video => "视频",
    };
    match item.size_bytes {
        Some(bytes) => format!("{kind} {}", size_label(bytes)),
        None => kind.into(),
    }
}
pub fn panel(shell: &Shell, s: &UiSnapshot, cx: &mut Context<Shell>) -> Div {
    let enabled = !s.busy && s.persistent_disabled.is_none();
    let target = shell.target(s);
    let panel = column()
        .w(px(INSPECTOR))
        .h_full()
        .flex_shrink_0()
        .bg(rgb(PANEL))
        .border_l_1()
        .border_color(rgb(BORDER));
    let Some(entry) = s
        .items
        .iter()
        .find(|i| Some(&i.item.id) == shell.selected.as_ref())
    else {
        return panel
            .justify_center()
            .items_center()
            .text_color(rgb(MUTED))
            .child(if s.items.is_empty() {
                "导入壁纸后在这里查看详情"
            } else {
                "选择壁纸查看详情"
            });
    };
    let item = &entry.item;
    let mut chips = row().flex_wrap().justify_center().gap(px(6.));
    if let (Some(w), Some(h)) = (item.width, item.height) {
        chips = chips.child(chip(format!("{w} × {h}")));
    }
    if item.kind == Kind::Video
        && let Some(duration) = item.duration_sec
    {
        chips = chips.child(chip(duration_label(duration)));
    }
    if let Some(date) = item.added_at.split('T').next().filter(|d| !d.is_empty()) {
        chips = chips.child(chip(format!("添加于 {date}")));
    }
    let fits = [
        (Fit::Fill, "填充"),
        (Fit::Fit, "适应"),
        (Fit::Stretch, "拉伸"),
    ]
    .into_iter()
    .map(|(fit, label)| {
        (
            label.into(),
            item.fit == fit,
            send(UiCommand::Fit(item.id.clone(), fit)),
        )
    })
    .collect();
    panel
        .child(
            column()
                .id("inspector-scroll")
                .flex_1()
                .min_h_0()
                .overflow_y_scroll()
                .p(px(16.))
                .gap(px(12.))
                .child(
                    div()
                        .w_full()
                        .h(px(preview_height((item.width, item.height))))
                        .flex_shrink_0()
                        .overflow_hidden()
                        .border_1()
                        .border_color(rgb(BORDER))
                        .child(thumbnail(entry)),
                )
                .child(
                    div()
                        .text_size(px(16.))
                        .text_center()
                        .font_weight(FontWeight::SEMIBOLD)
                        .child(item.title.clone()),
                )
                .child(
                    div()
                        .text_center()
                        .text_color(rgb(MUTED))
                        .child(summary(item)),
                )
                .child(chips)
                .child(shell.action(
                    "delete",
                    Some("trash"),
                    "删除",
                    UiCommand::PreviewDelete(item.id.clone()),
                    !s.busy,
                    Button::Danger,
                ))
                .child(section(None, "属性").mt(px(8.)))
                .child(
                    column()
                        .child(
                            property("image", "对齐方式")
                                .child(shell.dropdown("fit", 130., fits, enabled, cx)),
                        )
                        .child(
                            property("monitor", "目标显示器").child(
                                div()
                                    .max_w(px(130.))
                                    .truncate()
                                    .text_color(rgb(MUTED))
                                    .child(match target {
                                        Some((_, display)) => display.name.clone(),
                                        None => "无".into(),
                                    }),
                            ),
                        ),
                ),
        )
        .child(
            row()
                .p(px(12.))
                .gap(px(8.))
                .flex_shrink_0()
                .border_t_1()
                .border_color(rgb(BORDER))
                .when_some(target, |v, (number, display)| {
                    let applied = s.assigned(display).is_some_and(|i| i.item.id == item.id);
                    v.child(
                        shell
                            .action(
                                "assign-display",
                                None,
                                if applied {
                                    format!("已应用到显示器 {number}")
                                } else {
                                    format!("应用到显示器 {number}")
                                },
                                UiCommand::AssignDisplay(item.id.clone(), display.id.clone()),
                                enabled && !applied,
                                Button::Primary,
                            )
                            .flex_1(),
                    )
                })
                .when(s.displays.len() > 1 || target.is_none(), |v| {
                    v.child(
                        shell
                            .action(
                                "assign-all",
                                None,
                                "全部显示器",
                                UiCommand::AssignAll(item.id.clone()),
                                enabled,
                                Button::Outline,
                            )
                            .flex_1(),
                    )
                }),
        )
}
#[cfg(test)]
mod tests {
    use super::{INSPECTOR, duration_label, preview_height};
    #[test]
    fn duration_uses_clock_format() {
        assert_eq!(duration_label(0.4), "0:01");
        assert_eq!(duration_label(83.2), "1:23");
        assert_eq!(duration_label(3725.), "1:02:05");
    }
    #[test]
    fn preview_follows_aspect_within_limits() {
        let width = INSPECTOR - 32.;
        assert_eq!(preview_height((Some(1920), Some(1080))), width * 9. / 16.);
        assert_eq!(preview_height((Some(1080), Some(1920))), width);
        assert_eq!(preview_height((Some(4000), Some(400))), 120.);
        assert_eq!(preview_height((None, None)), width * 9. / 16.);
    }
}
