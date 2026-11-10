//! Stable identity rules shared by native enumeration and platform-independent tests.
use sha2::{Digest, Sha256};
use wallpaper_core::displays::DisplayId;

pub fn hash_id(prefix: &str, value: &str) -> DisplayId {
    let sum = Sha256::digest(value.as_bytes());
    let suffix: String = sum[..6].iter().map(|b| format!("{b:02x}")).collect();
    DisplayId(format!("{prefix}{suffix}"))
}
pub fn norm_path(path: &str) -> String {
    path.trim().to_lowercase()
}
pub fn is_interface_path(path: &str) -> bool {
    let path = path.trim();
    path.starts_with(r"\\?\") && path.len() > 8
}
pub fn clean_name(name: &str) -> String {
    let name = name.trim();
    if [
        "generic pnp monitor",
        "通用即插即用监视器",
        "generic non-pnp monitor",
        "通用非即插即用监视器",
        "default monitor",
        "默认监视器",
    ]
    .contains(&name.to_lowercase().as_str())
    {
        String::new()
    } else {
        name.into()
    }
}
pub fn mac_id(uuid: Option<&str>, number: u32) -> DisplayId {
    hash_id("m-", uuid.unwrap_or(&number.to_string()))
}
#[derive(Default)]
pub struct DcTarget {
    pub friendly: String,
    pub path: String,
}
#[derive(Default)]
pub struct MonIdentity {
    pub gdi: String,
    pub dc: Option<DcTarget>,
    pub edd_path: String,
    pub edd_string: String,
    pub wall_path: String,
}
impl MonIdentity {
    pub fn resolve(&self) -> (DisplayId, String, Vec<DisplayId>) {
        let key = [
            self.dc.as_ref().map_or("", |d| d.path.as_str()),
            &self.edd_path,
            &self.wall_path,
        ]
        .into_iter()
        .find(|p| is_interface_path(p));
        let id = match key {
            Some(key) => hash_id("w-", &norm_path(key)),
            None => hash_id("w-gdi-", &self.gdi.trim().to_uppercase()),
        };
        let mut name = self
            .dc
            .as_ref()
            .map_or_else(String::new, |d| clean_name(&d.friendly));
        if name.is_empty() {
            name = clean_name(&self.edd_string);
        }
        let legacy = [&self.wall_path, &self.gdi]
            .into_iter()
            .filter(|p| !p.is_empty())
            .map(|p| hash_id("w-", p))
            .filter(|old| old != &id)
            .collect();
        (id, name, legacy)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    fn identity() -> MonIdentity {
        MonIdentity {
            gdi: r"\\.\DISPLAY1".into(),
            dc: Some(DcTarget {
                path: r"  \\?\DISPLAY#DC#123  ".into(),
                friendly: "  Dell U2720Q  ".into(),
            }),
            edd_path: r"\\?\display#edd#456".into(),
            edd_string: "Fallback".into(),
            wall_path: r"\\?\DISPLAY#Wall#789".into(),
        }
    }
    // Expected digests computed independently with Python hashlib.sha256.
    #[test]
    fn windows_priority_normalization_and_legacy() {
        let mut m = identity();
        let (id, name, legacy) = m.resolve();
        assert_eq!(id.0, "w-7bf55c964d8a");
        assert_eq!(name, "Dell U2720Q");
        assert_eq!(
            legacy,
            vec![
                DisplayId("w-f25a6be46a6e".into()),
                DisplayId("w-fc2b61e60bd4".into())
            ]
        );
        m.dc.as_mut().unwrap().path = r"\\?\display#dc#123".into();
        assert_eq!(m.resolve().0.0, "w-7bf55c964d8a");
        m.dc = None;
        assert_eq!(m.resolve().0.0, "w-975ef38962be");
        assert_eq!(m.resolve().1, "Fallback");
        m.edd_path = r"\\.\DISPLAY1".into();
        assert_eq!(m.resolve().0.0, "w-aa9f8ed1cddd");
        m.wall_path = r"\\?\display#wall#789".into();
        assert_eq!(m.resolve().2, vec![DisplayId("w-fc2b61e60bd4".into())]);
        m.wall_path.clear();
        assert_eq!(m.resolve().0.0, "w-gdi-fc2b61e60bd4");
        m.gdi = r"  \\.\display1  ".into();
        assert_eq!(m.resolve().0.0, "w-gdi-fc2b61e60bd4");
        assert_eq!(m.resolve().2, vec![DisplayId("w-6ae5ca6c1f42".into())]);
    }
    #[test]
    fn names_and_interface_validation() {
        for name in [
            " Generic PnP Monitor ",
            "通用即插即用监视器",
            "generic non-pnp monitor",
            "通用非即插即用监视器",
            "default monitor",
            "默认监视器",
        ] {
            assert_eq!(clean_name(name), "");
        }
        let mut m = identity();
        m.dc.as_mut().unwrap().friendly = " Generic PnP Monitor ".into();
        assert_eq!(m.resolve().1, "Fallback");
        m.edd_string = "默认监视器".into();
        assert_eq!(m.resolve().1, "");
        for path in ["", "  ", r"\\.\DISPLAY1", r"\\?\1234"] {
            assert!(!is_interface_path(path));
        }
        assert!(is_interface_path(r" \\?\12345 "));
        assert_eq!(norm_path(" ABC "), "abc");
    }
    #[test]
    fn mac_uuid_and_decimal_fallback() {
        assert_eq!(
            mac_id(Some("12345678-1234-5678-ABCD-123456789ABC"), 42).0,
            "m-13e232a23d22"
        );
        assert_eq!(mac_id(None, 42).0, "m-73475cb40a56");
    }
}
