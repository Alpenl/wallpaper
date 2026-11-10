use serde::{Deserialize, Serialize};

pub const SCHEMA_VERSION: u32 = 1;

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Library {
    pub schema_version: u32,
    pub items: Vec<Item>,
}

impl Default for Library {
    fn default() -> Self {
        Self {
            schema_version: SCHEMA_VERSION,
            items: Vec::new(),
        }
    }
}

impl Library {
    pub fn find(&self, id: &str) -> Option<&Item> {
        self.items.iter().find(|item| item.id == id)
    }
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Item {
    pub id: String,
    pub title: String,
    pub kind: Kind,
    pub entry: String,
    pub width: Option<u32>,
    pub height: Option<u32>,
    pub duration_sec: Option<f64>,
    pub size_bytes: Option<u64>,
    pub added_at: String,
    #[serde(default)]
    pub fit: Fit,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Kind {
    Video,
    Image,
}

#[derive(Debug, Clone, Copy, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Fit {
    #[default]
    Fill,
    Fit,
    Stretch,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Media {
    pub path: String,
    pub kind: Kind,
    pub fit: Fit,
}

pub fn search<'a>(items: &'a [Item], query: &str) -> Vec<&'a Item> {
    let query = query.trim().to_lowercase();
    items
        .iter()
        .filter(|item| item.title.to_lowercase().contains(&query))
        .collect()
}

pub fn is_safe_component(s: &str) -> bool {
    !s.is_empty() && !s.starts_with('.') && !s.contains(['/', '\\', '\0', ':'])
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Probe {
    pub kind: Kind,
    pub width: u32,
    pub height: u32,
    pub duration_sec: Option<f64>,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct Rejection {
    pub code: RejectCode,
    pub detail: String,
}
impl Rejection {
    pub fn new(code: RejectCode, detail: impl ToString) -> Self {
        Self {
            code,
            detail: detail.to_string(),
        }
    }
}
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum RejectCode {
    InvalidEntry,
    UnsupportedExtension,
    NotAFile,
    EmptyFile,
    CopyFailed,
    DecodeFailed,
    NoVideoTrack,
    AnimatedImage,
    TooLarge,
    Timeout,
    Crashed,
    StorageFailed,
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn components() {
        for s in ["", ".", "..", ".hidden", "a/b", "a\\b", "a\0b", "C:", "a:b"] {
            assert!(!is_safe_component(s), "{s:?}");
        }
        for s in ["abc", "你好 世界", "a.b", "a-b_1"] {
            assert!(is_safe_component(s));
        }
    }
    #[test]
    fn unicode_search_preserves_order() {
        let items: Vec<_> = ["ÄBC 中国", "other", "äbc"]
            .into_iter()
            .map(|title| Item {
                id: title.into(),
                title: title.into(),
                kind: Kind::Image,
                entry: String::new(),
                width: None,
                height: None,
                duration_sec: None,
                size_bytes: None,
                added_at: String::new(),
                fit: Fit::Fill,
            })
            .collect();
        assert_eq!(search(&items, "  ").len(), 3);
        assert_eq!(search(&items, " ÄbC "), vec![&items[0], &items[2]]);
        assert_eq!(search(&items, "中国"), vec![&items[0]]);
        assert!(search(&items, "absent").is_empty());
    }
    #[test]
    fn media_wire_format() {
        let probe = Probe {
            kind: Kind::Image,
            width: 2,
            height: 3,
            duration_sec: None,
        };
        assert_eq!(
            serde_json::to_value(&probe).unwrap(),
            serde_json::json!({"kind":"image","width":2,"height":3,"durationSec":null})
        );
        for (code, name) in [
            (RejectCode::InvalidEntry, "invalid_entry"),
            (RejectCode::UnsupportedExtension, "unsupported_extension"),
            (RejectCode::NotAFile, "not_a_file"),
            (RejectCode::EmptyFile, "empty_file"),
            (RejectCode::CopyFailed, "copy_failed"),
            (RejectCode::DecodeFailed, "decode_failed"),
            (RejectCode::NoVideoTrack, "no_video_track"),
            (RejectCode::AnimatedImage, "animated_image"),
            (RejectCode::TooLarge, "too_large"),
            (RejectCode::Timeout, "timeout"),
            (RejectCode::Crashed, "crashed"),
            (RejectCode::StorageFailed, "storage_failed"),
        ] {
            let rejection = Rejection::new(code, "detail");
            let value = serde_json::to_value(&rejection).unwrap();
            assert_eq!(value["code"], name);
            assert_eq!(
                serde_json::from_value::<Rejection>(value).unwrap(),
                rejection
            );
        }
    }
}
