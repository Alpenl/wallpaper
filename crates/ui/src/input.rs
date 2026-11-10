//! Search field. The caret always sits at the end of the text, which is enough for a
//! filter box and keeps IME composition (marked text) correct.
use crate::{icons::icon, shell::Shell, style::*};
use gpui::{prelude::*, *};
use std::ops::Range;

fn utf16_len(text: &str) -> usize {
    text.encode_utf16().count()
}
fn byte_offset(text: &str, utf16: usize) -> usize {
    let mut units = 0;
    for (index, c) in text.char_indices() {
        if units >= utf16 {
            return index;
        }
        units += c.len_utf16();
    }
    text.len()
}
impl Shell {
    fn replace(&mut self, range: Option<Range<usize>>, text: &str) -> usize {
        let end = utf16_len(&self.query);
        let range = range.or(self.marked.clone()).unwrap_or(end..end);
        let start = byte_offset(&self.query, range.start);
        let stop = byte_offset(&self.query, range.end).max(start);
        self.query.replace_range(start..stop, text);
        range.start
    }
}
impl EntityInputHandler for Shell {
    fn text_for_range(
        &mut self,
        range: Range<usize>,
        _: &mut Option<Range<usize>>,
        _: &mut Window,
        _: &mut Context<Self>,
    ) -> Option<String> {
        let start = byte_offset(&self.query, range.start);
        let stop = byte_offset(&self.query, range.end).max(start);
        Some(self.query[start..stop].to_string())
    }
    fn selected_text_range(
        &mut self,
        _: bool,
        _: &mut Window,
        _: &mut Context<Self>,
    ) -> Option<UTF16Selection> {
        let end = utf16_len(&self.query);
        Some(UTF16Selection {
            range: end..end,
            reversed: false,
        })
    }
    fn marked_text_range(&self, _: &mut Window, _: &mut Context<Self>) -> Option<Range<usize>> {
        self.marked.clone()
    }
    fn unmark_text(&mut self, _: &mut Window, _: &mut Context<Self>) {
        self.marked = None;
    }
    fn replace_text_in_range(
        &mut self,
        range: Option<Range<usize>>,
        text: &str,
        _: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.replace(range, text);
        self.marked = None;
        cx.notify();
    }
    fn replace_and_mark_text_in_range(
        &mut self,
        range: Option<Range<usize>>,
        text: &str,
        _: Option<Range<usize>>,
        _: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let start = self.replace(range, text);
        self.marked = (!text.is_empty()).then(|| start..start + utf16_len(text));
        cx.notify();
    }
    fn bounds_for_range(
        &mut self,
        _: Range<usize>,
        bounds: Bounds<Pixels>,
        _: &mut Window,
        _: &mut Context<Self>,
    ) -> Option<Bounds<Pixels>> {
        Some(bounds)
    }
    fn character_index_for_point(
        &mut self,
        _: Point<Pixels>,
        _: &mut Window,
        _: &mut Context<Self>,
    ) -> Option<usize> {
        None
    }
}
pub fn render(shell: &Shell, window: &Window, cx: &mut Context<Shell>) -> Stateful<Div> {
    let focused = shell.focus.is_focused(window);
    let focus = shell.focus.clone();
    let entity = cx.entity();
    // Roughly what fits beside the icon and clear button; past it, keep the caret in view.
    let overflowing = shell
        .query
        .chars()
        .map(|c| if c.is_ascii() { 7.5 } else { 14. })
        .sum::<f32>()
        > 215.;
    row()
        .id("search")
        .relative()
        .track_focus(&shell.focus)
        .w(px(280.))
        .h(px(CONTROL))
        .px(px(8.))
        .gap(px(6.))
        .flex_shrink_0()
        .bg(rgb(CARD))
        .border_1()
        .border_color(rgb(if focused { QUIET } else { BORDER }))
        .cursor_text()
        .child(icon("search", 15., MUTED))
        .child(
            row()
                .flex_1()
                .min_w_0()
                .overflow_hidden()
                .when(overflowing, |s| s.justify_end())
                .when(shell.query.is_empty() && !focused, |s| {
                    s.text_color(rgb(MUTED)).child("搜索")
                })
                .when(!shell.query.is_empty(), |s| {
                    s.child(div().flex_shrink_0().child(shell.query.clone()))
                })
                .when(focused, |s| {
                    s.child(div().w(px(1.)).h(px(15.)).flex_shrink_0().bg(rgb(TEXT)))
                }),
        )
        .when(!shell.query.is_empty(), |s| {
            s.child(
                div()
                    .id("search-clear")
                    .cursor_pointer()
                    .child(icon("close", 12., MUTED))
                    .on_click(cx.listener(|this, _, _, cx| {
                        this.query.clear();
                        this.marked = None;
                        cx.notify();
                    })),
            )
        })
        .child(
            canvas(
                |_, _, _| (),
                move |bounds, _, window, cx| {
                    window.handle_input(&focus, ElementInputHandler::new(bounds, entity), cx);
                },
            )
            .absolute()
            .size_full(),
        )
        .on_key_down(cx.listener(|this, event: &KeyDownEvent, _, cx| {
            if this.marked.is_some() {
                return;
            }
            let command = event.keystroke.modifiers.secondary(cx);
            match event.keystroke.key.as_str() {
                "backspace" if command => this.query.clear(),
                "backspace" => {
                    this.query.pop();
                }
                "v" if command => {
                    if let Some(text) = cx.read_from_clipboard().and_then(|c| c.text()) {
                        this.query.push_str(&text.replace(['\r', '\n'], " "));
                    }
                }
                "escape" => this.query.clear(),
                _ => return,
            }
            cx.stop_propagation();
            cx.notify();
        }))
        .on_click(cx.listener(|this, _, window, cx| {
            window.focus(&this.focus, cx);
            cx.notify();
        }))
}
