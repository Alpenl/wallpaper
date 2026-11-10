use std::path::Path;
use wallpaper_core::library::Kind;
pub use wallpaper_core::library::{Probe, RejectCode, Rejection};

/// Runs system decoders. Call only in the isolated --media-probe process.
pub fn probe(path: &Path, kind: Kind, thumbnail: Option<&Path>) -> Result<Probe, Rejection> {
    #[cfg(target_os = "macos")]
    {
        crate::mac::probe::probe(path, kind, thumbnail)
    }
    #[cfg(windows)]
    {
        crate::win::probe::probe(path, kind, thumbnail)
    }
}
