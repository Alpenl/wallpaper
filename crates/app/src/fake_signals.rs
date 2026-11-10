//! Compiled only in debug builds. The probe never calls this module.
use anyhow::{Context, Result, ensure};
use serde::Deserialize;
use std::{collections::BTreeMap, io::Read, path::Path};
use wallpaper_core::{Truth, displays::DisplayId, policy::Power};
use wallpaper_platform::Visibility;

#[derive(Default, Deserialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct Fake {
    locked: Option<bool>,
    asleep: Option<bool>,
    // Value preserves the distinction between an absent key and explicit null.
    #[serde(default, deserialize_with = "power_field")]
    power: Option<Option<Power>>,
    #[serde(default)]
    displays: BTreeMap<String, Display>,
}
fn power_field<'de, D: serde::Deserializer<'de>>(d: D) -> Result<Option<Option<Power>>, D::Error> {
    Option::<Power>::deserialize(d).map(Some)
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Display {
    fullscreen: Option<Truth>,
    covered: Option<Truth>,
}
impl Fake {
    pub(crate) fn read(path: &Path) -> Result<Self> {
        let file = std::fs::File::open(path).context("fake signals: open file")?;
        let mut bytes = Vec::new();
        file.take(65537)
            .read_to_end(&mut bytes)
            .context("fake signals: read file")?;
        ensure!(bytes.len() <= 65536, "fake signals: file exceeds 64 KiB");
        let value: Self = serde_json::from_slice(&bytes).context("fake signals: invalid JSON")?;
        ensure!(
            value
                .power
                .flatten()
                .and_then(|p| p.percent)
                .is_none_or(|p| p <= 100),
            "fake signals: percent must be 0..=100"
        );
        Ok(value)
    }
    pub(crate) fn apply(
        &self,
        locked: &mut bool,
        asleep: &mut bool,
        power: &mut Option<Power>,
        displays: &mut BTreeMap<DisplayId, Visibility>,
    ) {
        if let Some(v) = self.locked {
            *locked = v;
        }
        if let Some(v) = self.asleep {
            *asleep = v;
        }
        if let Some(v) = self.power {
            *power = v;
        }
        for (id, value) in displays {
            if let Some(fake) = self.displays.get(&id.0) {
                if let Some(v) = fake.fullscreen {
                    value.fullscreen = v;
                }
                if let Some(v) = fake.covered {
                    value.covered = v;
                }
            }
        }
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn overrides_preserve_absent_and_null_and_unknown() {
        let fake: Fake = serde_json::from_str(
            r#"{"power":null,"locked":true,"displays":{"a":{"covered":"unknown"}}}"#,
        )
        .unwrap();
        let (mut locked, mut asleep, mut power) = (
            false,
            true,
            Some(Power {
                on_battery: true,
                percent: Some(50),
            }),
        );
        let mut displays = BTreeMap::from([(
            DisplayId("a".into()),
            Visibility {
                fullscreen: Truth::Yes,
                covered: Truth::Yes,
                coverage: Some(1.0),
            },
        )]);
        fake.apply(&mut locked, &mut asleep, &mut power, &mut displays);
        assert!(locked && asleep);
        assert_eq!(power, None);
        assert_eq!(displays[&DisplayId("a".into())].covered, Truth::Unknown);
        assert_eq!(displays[&DisplayId("a".into())].fullscreen, Truth::Yes);
        assert_eq!(serde_json::from_str::<Fake>("{}").unwrap().power, None);
    }
    #[test]
    fn reread_missing_invalid_and_bounded_file() {
        let dir = std::env::temp_dir().join(format!("wp-signals-{}", std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        let file = dir.join("fake.json");
        std::fs::write(&file, b"{}").unwrap();
        assert!(Fake::read(&file).is_ok());
        for invalid in [
            b"bad".to_vec(),
            vec![b' '; 65537],
            br#"{"power":{"on_battery":true,"percent":101}}"#.to_vec(),
            br#"{"displays":{"a":{"covered":"bad"}}}"#.to_vec(),
        ] {
            std::fs::write(&file, invalid).unwrap();
            assert!(Fake::read(&file).is_err());
        }
        std::fs::remove_file(&file).unwrap();
        assert!(Fake::read(&file).is_err());
        std::fs::remove_dir(dir).unwrap();
    }
}
