//! Hand-drawn SVG glyphs in the Sooty style: 24px grid, 1.6 stroke, tinted by text color.
use gpui::{prelude::*, *};
use std::borrow::Cow;

pub struct Icons;

const GLYPHS: &[(&str, &str)] = &[
    ("plus", r#"<path d="M12 4v16M4 12h16"/>"#),
    ("play", r#"<path d="m8 4 12 8-12 8z"/>"#),
    ("pause", r#"<path d="M8 4v16M16 4v16"/>"#),
    ("close", r#"<path d="m5 5 14 14M19 5 5 19"/>"#),
    ("chevron", r#"<path d="m5 9 7 7 7-7"/>"#),
    ("ascending", r#"<path d="m5 15 7-7 7 7"/>"#),
    (
        "search",
        r#"<circle cx="10" cy="10" r="6.5"/><path d="m15 15 6 6"/>"#,
    ),
    (
        "trash",
        r#"<path d="M5 6h14l-1 15H6zM3 6h18M9 3h6M10 10v7m4-7v7"/>"#,
    ),
    (
        "folder",
        r#"<path d="M2 6h8l2 3h10v11H2z" fill="white" stroke="none"/><path d="M3 4h7l2 2h8"/>"#,
    ),
    (
        "gear",
        r#"<path d="m10 2-1 3-3 1-3 4 1 3-1 3 3 3 3 1 1 2h4l1-2 3-1 3-3-1-3 1-3-3-4-3-1-1-3Z"/><circle cx="12" cy="12" r="3"/>"#,
    ),
    (
        "monitor",
        r#"<rect x="2" y="4" width="20" height="13" rx="1"/><path d="M8 21h8M12 17v4"/>"#,
    ),
    (
        "image",
        r#"<rect x="3" y="4" width="18" height="16" rx="1"/><path d="m3 16 5-5 4 4 3-3 6 6"/><circle cx="15.5" cy="8.5" r="1.2"/>"#,
    ),
    ("power", r#"<path d="M12 3v9M7 6a8 8 0 1 0 10 0"/>"#),
    (
        "info",
        r#"<circle cx="12" cy="12" r="9"/><path d="M12 11v6m0-9v.5"/>"#,
    ),
];

impl AssetSource for Icons {
    fn load(&self, path: &str) -> Result<Option<Cow<'static, [u8]>>> {
        Ok(GLYPHS.iter().find(|(name, _)| *name == path).map(|(_, body)| {
            Cow::Owned(format!(
                r#"<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="white" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">{body}</svg>"#
            ).into_bytes())
        }))
    }
    fn list(&self, _: &str) -> Result<Vec<SharedString>> {
        Ok(GLYPHS.iter().map(|(name, _)| (*name).into()).collect())
    }
}

pub fn icon(name: &'static str, size: f32, color: u32) -> Svg {
    svg()
        .path(name)
        .size(px(size))
        .text_color(rgb(color))
        .flex_shrink_0()
}
