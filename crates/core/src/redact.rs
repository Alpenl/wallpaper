//! Pure redaction: collect ranges on the original input, merge, replace once.
#[derive(Debug, PartialEq, Eq)]
pub enum Omitted {
    InvalidUnicode,
    Unsafe,
}

use std::collections::{BTreeMap, BTreeSet, HashMap};

struct Pattern {
    text: String,
    bounded: bool,
}
#[derive(Default)]
struct Index {
    prefixes: HashMap<[u8; 3], Vec<Pattern>>,
    short: Vec<Pattern>,
}
impl Index {
    fn insert(&mut self, pattern: Pattern) {
        if let Some(prefix) = pattern.text.as_bytes().get(..3) {
            self.prefixes
                .entry(prefix.try_into().unwrap())
                .or_default()
                .push(pattern);
        } else {
            self.short.push(pattern);
        }
    }
    fn matches(
        &self,
        line: &str,
        start: usize,
        insensitive: bool,
        ranges: &mut Vec<(usize, usize)>,
    ) {
        let suffix = &line.as_bytes()[start..];
        let bucket = suffix.get(..3).and_then(|bytes| {
            let mut prefix: [u8; 3] = bytes.try_into().unwrap();
            if insensitive {
                prefix.make_ascii_lowercase();
            }
            self.prefixes.get(&prefix)
        });
        for pattern in bucket.into_iter().flatten().chain(&self.short) {
            let Some(candidate) = suffix.get(..pattern.text.len()) else {
                continue;
            };
            let matches = if insensitive {
                candidate.eq_ignore_ascii_case(pattern.text.as_bytes())
            } else {
                candidate == pattern.text.as_bytes()
            };
            let end = start + pattern.text.len();
            if matches
                && (!pattern.bounded || (boundary_before(line, start) && boundary_after(line, end)))
            {
                ranges.push((start, end));
            }
        }
    }
}

/// Compile known-value variants once and reuse for every line in an export.
#[derive(Default)]
pub struct Redactor {
    sensitive: Index,
    insensitive: Index,
}
impl Redactor {
    pub fn new(known: &[String]) -> Self {
        let mut forms = BTreeMap::new();
        for value in known.iter().collect::<BTreeSet<_>>() {
            let length = value.chars().count();
            if length < 2 {
                continue;
            }
            let insensitive = value.contains('\\') || value.as_bytes().get(1) == Some(&b':');
            for mut form in variants(value) {
                if insensitive {
                    form.make_ascii_lowercase();
                }
                // If any original value allows an unbounded match, preserve it.
                forms
                    .entry((form, insensitive))
                    .and_modify(|bounded| *bounded &= length <= 3)
                    .or_insert(length <= 3);
            }
        }
        let mut result = Self::default();
        for ((text, insensitive), bounded) in forms {
            let index = if insensitive {
                &mut result.insensitive
            } else {
                &mut result.sensitive
            };
            index.insert(Pattern { text, bounded });
        }
        result
    }

    pub fn redact(&self, line: &str) -> Result<String, Omitted> {
        let mut ranges = Vec::new();
        let bytes = line.as_bytes();
        let mut generic_end = 0;
        for (i, c) in line.char_indices() {
            if c == '\u{fffd}' {
                return Err(Omitted::InvalidUnicode);
            }
            if c.is_control() && c != '\t' {
                return Err(Omitted::Unsafe);
            }
            self.sensitive.matches(line, i, false, &mut ranges);
            self.insensitive.matches(line, i, true, &mut ranges);
            if i < generic_end {
                continue;
            }
            // URL schemes (including file:) are removed in full.
            if c.is_ascii_alphabetic() && boundary_before(line, i) {
                let mut end = i;
                while end < bytes.len()
                    && (bytes[end].is_ascii_alphanumeric() || b"+.-".contains(&bytes[end]))
                {
                    end += 1;
                }
                let file = line[i..]
                    .get(..5)
                    .is_some_and(|s| s.eq_ignore_ascii_case("file:"));
                if file || line[end..].starts_with("://") {
                    generic_end = if file {
                        path_end(line, i)?
                    } else {
                        token_end(line, i)
                    };
                    ranges.push((i, generic_end));
                    continue;
                }
            }
            let allowed = i == 0
                || line[..i]
                    .chars()
                    .next_back()
                    .is_some_and(|p| p.is_whitespace() || "\"'=([:".contains(p));
            let unix = c == '/'
                && allowed
                && bytes
                    .get(i + 1)
                    .is_some_and(|b| !b.is_ascii_whitespace() && *b != b'/');
            let home = line[i..].starts_with("~/");
            let drive = c.is_ascii_alphabetic()
                && bytes.get(i + 1) == Some(&b':')
                && bytes.get(i + 2).is_some_and(|b| *b == b'/' || *b == b'\\');
            let unc = line[i..].starts_with("\\\\") && allowed;
            if unix || home || drive || unc {
                generic_end = path_end(line, i)?;
                ranges.push((i, generic_end));
            }
            if c == '@' {
                let start = line[..i]
                    .rfind(|p: char| !(p.is_ascii_alphanumeric() || "._%+-".contains(p)))
                    .map_or(0, |n| n + line[n..].chars().next().unwrap().len_utf8());
                let end = line[i + 1..]
                    .find(|p: char| !(p.is_ascii_alphanumeric() || ".-".contains(p)))
                    .map_or(line.len(), |n| i + 1 + n);
                if start < i && line[i + 1..end].contains('.') {
                    ranges.push((start, end));
                }
            }
        }
        ranges.sort_unstable();
        let mut merged: Vec<(usize, usize)> = Vec::new();
        for (start, end) in ranges {
            if let Some(last) = merged.last_mut()
                && start <= last.1
            {
                last.1 = last.1.max(end);
                continue;
            }
            merged.push((start, end));
        }
        let mut result = String::new();
        let mut cursor = 0;
        for (start, end) in merged {
            result.push_str(&line[cursor..start]);
            result.push_str("[redacted]");
            cursor = end;
        }
        result.push_str(&line[cursor..]);
        Ok(result)
    }
}

pub fn redact(line: &str, known: &[String]) -> Result<String, Omitted> {
    Redactor::new(known).redact(line)
}

fn boundary_before(s: &str, i: usize) -> bool {
    s[..i]
        .chars()
        .next_back()
        .is_none_or(|c| !(c.is_alphanumeric() || c == '_'))
}
fn boundary_after(s: &str, i: usize) -> bool {
    s[i..]
        .chars()
        .next()
        .is_none_or(|c| !(c.is_alphanumeric() || c == '_'))
}
fn variants(value: &str) -> Vec<String> {
    let mut forms = Vec::new();
    for form in [
        value.to_owned(),
        value.replace('\\', "/"),
        value.replace('/', "\\"),
    ] {
        let json = serde_json::to_string(&form).unwrap();
        forms.push(json[1..json.len() - 1].to_owned());
        // Both full percent encoding and normal URI path encoding.
        for all in [false, true] {
            let encoded: String = form
                .bytes()
                .map(|b| {
                    if b.is_ascii_alphanumeric()
                        || b"-._~".contains(&b)
                        || (!all && b"/:".contains(&b))
                    {
                        (b as char).to_string()
                    } else {
                        format!("%{b:02X}")
                    }
                })
                .collect();
            forms.push(encoded.clone());
            let mut lower = encoded.into_bytes();
            for i in 0..lower.len().saturating_sub(2) {
                if lower[i] == b'%' {
                    lower[i + 1].make_ascii_lowercase();
                    lower[i + 2].make_ascii_lowercase();
                }
            }
            forms.push(String::from_utf8(lower).unwrap());
        }
        forms.push(form);
    }
    forms.sort();
    forms.dedup();
    forms
}
fn token_end(s: &str, start: usize) -> usize {
    s[start..]
        .find(|c: char| c.is_whitespace() || c == '"' || c == '\'')
        .map_or(s.len(), |n| start + n)
}
fn path_end(s: &str, start: usize) -> Result<usize, Omitted> {
    // Only quotes immediately before the path delimit it. Unquoted paths may
    // contain spaces, so conservatively remove the remainder of the line.
    let prefix = &s[..start];
    let quote = prefix
        .chars()
        .next_back()
        .filter(|c| *c == '"' || *c == '\'');
    if let Some(quote) = quote {
        let escaped = prefix.ends_with("\\\"");
        let suffix = &s[start..];
        if escaped {
            return suffix
                .find("\\\"")
                .map(|n| start + n)
                .ok_or(Omitted::Unsafe);
        }
        for (n, c) in suffix.char_indices() {
            if c == quote {
                let slash_count = suffix[..n]
                    .bytes()
                    .rev()
                    .take_while(|b| *b == b'\\')
                    .count();
                if slash_count % 2 == 0 {
                    return Ok(start + n);
                }
            }
        }
        return Err(Omitted::Unsafe);
    }
    Ok(s.len())
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn email_multibyte_prefix_and_missing_local_part() {
        for (input, expected) in [
            ("邮箱@example.org here", "邮箱@example.org here"),
            ("邮箱alice@example.org here", "邮箱[redacted] here"),
            ("😀a@example.org here", "😀[redacted] here"),
            ("@example.org here", "@example.org here"),
            (" @example.org", " @example.org"),
        ] {
            assert_eq!(redact(input, &[]).unwrap(), expected);
        }
    }
    #[test]
    fn file_urls_use_path_end_rules_other_schemes_keep_token_end() {
        for (input, expected) in [
            ("file:///Users/a b/c.mp4", "[redacted]"),
            ("failed file:///Users/a b/c.mp4 more", "failed [redacted]"),
            ("\"file:///Users/a b/c.mp4\" more", "\"[redacted]\" more"),
            ("'FILE:///Users/a b/c.mp4' more", "'[redacted]' more"),
            (
                r#"{\"path\":\"file:///Users/a b/c.mp4\"}"#,
                r#"{\"path\":\"[redacted]\"}"#,
            ),
            ("https://example.org/a b/c.mp4", "[redacted] b/c.mp4"),
            ("mail=a.b@example.org", "mail=[redacted]"),
        ] {
            assert_eq!(redact(input, &[]).unwrap(), expected);
        }
    }
    #[test]
    fn compiled_forms_preserve_case_short_boundaries_and_overlaps() {
        let redactor = Redactor::new(&[
            "ab".into(),
            "abc".into(),
            "secret".into(),
            "secret-long".into(),
            "secret".into(),
            "雪山".into(),
            "a\"".into(),
            "C:\\Private Path".into(),
        ]);
        assert_eq!(
            redactor
                .redact("ab abc abcd xab secret-long secret 雪山 雪山人")
                .unwrap(),
            "[redacted] [redacted] abcd xab [redacted] [redacted] [redacted] 雪山人"
        );
        assert_eq!(
            redactor.redact(r#"a\" c:%5cprivate%20path"#).unwrap(),
            "[redacted] [redacted]"
        );
        assert_eq!(
            Redactor::new(&["Title".into()])
                .redact("title Title")
                .unwrap(),
            "title [redacted]"
        );
    }
    #[test]
    fn overlaps_and_placeholder_are_replaced_once() {
        assert_eq!(
            redact(
                "secret-long secret [redacted]",
                &["secret".into(), "secret-long".into(), "redacted".into()]
            )
            .unwrap(),
            "[redacted] [redacted] [[redacted]]"
        );
    }
    #[test]
    fn paths_json_spaces_case_and_encoding() {
        let values = vec![
            "C:\\Users\\Private Person\\movie.mp4".into(),
            "Private title".into(),
        ];
        for text in [
            r#"path="c:\users\private person\movie.mp4" done"#,
            r#"{"path":"C:\\Users\\Private Person\\movie.mp4"}"#,
            "Private%20title",
            "Private title",
        ] {
            let got = redact(text, &values).unwrap();
            assert!(!got.to_lowercase().contains("private"), "{got}");
        }
        assert_eq!(
            redact("path=/Users/some one/clip.mov other", &[]).unwrap(),
            "path=[redacted]"
        );
        assert_eq!(
            redact("path=\"/Users/some one/clip.mov\" done", &[]).unwrap(),
            "path=\"[redacted]\" done"
        );
        assert_eq!(
            redact(r"path=\\server\share\some file", &[]).unwrap(),
            "path=[redacted]"
        );
        assert_eq!(
            redact(r#"{"path":"C:\\some one\\clip.mov"}"#, &[]).unwrap(),
            r#"{"path":"[redacted]"}"#
        );
    }
    #[test]
    fn urls_emails_and_short_boundaries() {
        assert_eq!(
            redact(
                "https://example.org/x?q=secret file:///some/path mail=a.b@example.org",
                &[]
            )
            .unwrap(),
            "[redacted] [redacted]"
        );
        assert_eq!(
            redact(
                "ab abc abcd xab 1/2 a/b",
                &["ab".into(), "abc".into(), "x".into()]
            )
            .unwrap(),
            "[redacted] [redacted] abcd xab 1/2 a/b"
        );
        assert_eq!(redact("invalid �", &[]), Err(Omitted::InvalidUnicode));
        assert_eq!(redact("\"/unterminated", &[]), Err(Omitted::Unsafe));
    }
}
