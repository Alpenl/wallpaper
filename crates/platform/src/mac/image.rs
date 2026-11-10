use anyhow::{Context, Result, anyhow};
use objc2::{class, msg_send, rc::Retained, runtime::AnyObject};
use objc2_core_foundation::{CFRetained, CFType};
use objc2_core_graphics::CGImage;
use objc2_foundation::{NSNumber, NSString, NSURL};
use std::{
    ffi::c_void,
    ptr::NonNull,
    sync::mpsc::{self, Receiver},
};
use wallpaper_core::fit::{decode_long_side, validate_image_size};

#[link(name = "ImageIO", kind = "framework")]
unsafe extern "C" {
    pub(super) fn CGImageSourceCreateWithURL(
        url: *const c_void,
        options: *const c_void,
    ) -> *mut CFType;
    pub(super) fn CGImageSourceCopyPropertiesAtIndex(
        source: *const CFType,
        index: usize,
        options: *const c_void,
    ) -> *mut CFType;
    pub(super) fn CGImageSourceCreateThumbnailAtIndex(
        source: *const CFType,
        index: usize,
        options: *const AnyObject,
    ) -> *mut CGImage;
    pub(super) static kCGImagePropertyPixelWidth: *const AnyObject;
    pub(super) static kCGImagePropertyPixelHeight: *const AnyObject;
    pub(super) static kCGImagePropertyOrientation: *const AnyObject;
    pub(super) static kCGImageSourceCreateThumbnailFromImageAlways: *const AnyObject;
    pub(super) static kCGImageSourceCreateThumbnailWithTransform: *const AnyObject;
    pub(super) static kCGImageSourceShouldCacheImmediately: *const AnyObject;
    pub(super) static kCGImageSourceThumbnailMaxPixelSize: *const AnyObject;
}

pub(super) struct Decoded(pub CFRetained<CGImage>);
// CGImage is immutable. The worker completes eager ImageIO decoding before
// handing off its sole owned reference; no AppKit/AV objects cross threads.
unsafe impl Send for Decoded {}

pub(super) fn start(path: String, width: u32, height: u32) -> Result<Receiver<Result<Decoded>>> {
    let (send, receive) = mpsc::channel();
    std::thread::Builder::new()
        .name("wallpaper-image".into())
        .spawn(move || {
            let result = objc2::rc::autoreleasepool(|_| decode(&path, width, height));
            // Closing the desktop drops the receiver. An unclaimed image is then
            // released here, without touching the closed window or blocking close.
            let _ = send.send(result);
        })
        .context("image: start decoder thread")?;
    Ok(receive)
}

pub(super) fn decode(path: &str, width: u32, height: u32) -> Result<Decoded> {
    unsafe {
        let url = NSURL::fileURLWithPath(&NSString::from_str(path));
        let source = NonNull::new(CGImageSourceCreateWithURL(
            (&*url as *const NSURL).cast(),
            std::ptr::null(),
        ))
        .context("image: ImageIO could not open source")?;
        let source = CFRetained::from_raw(source);
        let properties = NonNull::new(CGImageSourceCopyPropertiesAtIndex(
            &*source,
            0,
            std::ptr::null(),
        ))
        .context("image: missing first-frame properties")?;
        let properties = CFRetained::from_raw(properties);
        let dictionary = (&*properties as *const CFType).cast::<AnyObject>();
        let number = |key: *const AnyObject| -> u32 {
            let value: *const AnyObject = msg_send![dictionary, objectForKey: key];
            if value.is_null() {
                return 0;
            }
            let n: u64 = msg_send![value, unsignedLongLongValue];
            u32::try_from(n).unwrap_or(u32::MAX)
        };
        let mut iw = number(kCGImagePropertyPixelWidth);
        let mut ih = number(kCGImagePropertyPixelHeight);
        validate_image_size(iw, ih).map_err(|e| anyhow!(e))?;
        // ThumbnailWithTransform swaps axes for EXIF orientations 5 through 8.
        // Calculate the required long side in those displayed coordinates.
        if (5..=8).contains(&number(kCGImagePropertyOrientation)) {
            std::mem::swap(&mut iw, &mut ih);
        }
        let size = decode_long_side(iw, ih, width, height);
        anyhow::ensure!(size != 0, "image: invalid screen dimensions");
        let options: Retained<AnyObject> = msg_send![class!(NSMutableDictionary), new];
        let yes = NSNumber::numberWithBool(true);
        for key in [
            kCGImageSourceCreateThumbnailFromImageAlways,
            kCGImageSourceCreateThumbnailWithTransform,
            kCGImageSourceShouldCacheImmediately,
        ] {
            let _: () = msg_send![&*options, setObject: &*yes, forKey: key];
        }
        let size = NSNumber::numberWithUnsignedInt(size);
        let _: () =
            msg_send![&*options, setObject: &*size, forKey: kCGImageSourceThumbnailMaxPixelSize];
        let image = NonNull::new(CGImageSourceCreateThumbnailAtIndex(&*source, 0, &*options))
            .context("image: ImageIO thumbnail decode failed")?;
        Ok(Decoded(CFRetained::from_raw(image)))
    }
}

#[cfg(test)]
pub(super) mod tests {
    use super::*;
    use std::{
        path::PathBuf,
        sync::atomic::{AtomicU64, Ordering},
        time::Duration,
    };

    struct Fixture(PathBuf);
    impl Fixture {
        fn new(bytes: &[u8]) -> Self {
            static SERIAL: AtomicU64 = AtomicU64::new(0);
            let path = std::env::temp_dir().join(format!(
                "wp-image-{}-{}.tiff",
                std::process::id(),
                SERIAL.fetch_add(1, Ordering::Relaxed)
            ));
            std::fs::write(&path, bytes).unwrap();
            Self(path)
        }
        fn load(&self, w: u32, h: u32) -> Result<Decoded> {
            start(self.0.to_string_lossy().into_owned(), w, h)
                .unwrap()
                .recv_timeout(Duration::from_secs(10))
                .unwrap()
        }
    }
    impl Drop for Fixture {
        fn drop(&mut self) {
            std::fs::remove_file(&self.0).unwrap();
        }
    }
    // Minimal uncompressed RGB TIFF; oversized fixtures use a sparse file so
    // metadata is valid without allocating or decoding hundreds of MB.
    pub(crate) fn tiff(width: u32, height: u32, orientation: u16, pixels: bool) -> Vec<u8> {
        let entries: [(u16, u16, u32, u32); 10] = [
            (256, 4, 1, width),
            (257, 4, 1, height),
            (258, 3, 3, 134),
            (259, 3, 1, 1),
            (262, 3, 1, 2),
            (273, 4, 1, 140),
            (274, 3, 1, orientation.into()),
            (277, 3, 1, 3),
            (278, 4, 1, height),
            (279, 4, 1, width.saturating_mul(height).saturating_mul(3)),
        ];
        let mut data = b"II\x2a\x00\x08\x00\x00\x00".to_vec();
        data.extend_from_slice(&10u16.to_le_bytes());
        for (tag, kind, count, value) in entries {
            data.extend_from_slice(&tag.to_le_bytes());
            data.extend_from_slice(&kind.to_le_bytes());
            data.extend_from_slice(&count.to_le_bytes());
            data.extend_from_slice(&value.to_le_bytes());
        }
        data.extend_from_slice(&0u32.to_le_bytes());
        data.extend_from_slice(&[8, 0, 8, 0, 8, 0]);
        if pixels {
            data.resize(140 + (width * height * 3) as usize, 127);
        }
        data
    }
    #[test]
    fn background_decode_uses_fill_long_side_and_exif_axes() {
        let portrait = Fixture::new(&tiff(20, 80, 1, true));
        let image = portrait.load(19, 10).unwrap();
        assert_eq!(
            (
                CGImage::width(Some(&image.0)),
                CGImage::height(Some(&image.0))
            ),
            (19, 76)
        );
        for orientation in 5..=8 {
            let rotated = Fixture::new(&tiff(20, 80, orientation, true));
            let image = rotated.load(19, 10).unwrap();
            assert_eq!(
                (
                    CGImage::width(Some(&image.0)),
                    CGImage::height(Some(&image.0))
                ),
                (40, 10)
            );
        }
    }
    #[test]
    fn background_decode_reports_invalid_source_and_pixel_limit() {
        let invalid = Fixture::new(b"not an image");
        assert!(invalid.load(100, 100).is_err());
        let huge = Fixture::new(&tiff(20_001, 10_000, 1, false));
        std::fs::OpenOptions::new()
            .write(true)
            .open(&huge.0)
            .unwrap()
            .set_len(140 + 20_001 * 10_000 * 3)
            .unwrap();
        let error = huge.load(100, 100).err().unwrap().to_string();
        assert!(error.contains("MAX_IMAGE_PIXELS=200000000"), "{error}");
    }
    #[test]
    fn receiver_can_close_before_decoder_finishes() {
        let fixture = Fixture::new(&tiff(20, 80, 1, true));
        drop(start(fixture.0.to_string_lossy().into_owned(), 19, 10).unwrap());
        // A second real decode verifies the decoder path remains usable.
        assert!(fixture.load(19, 10).is_ok());
    }
}
