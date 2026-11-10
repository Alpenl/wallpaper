use super::image::*;
use anyhow::{Context, Result};
use objc2::{
    class, msg_send,
    rc::{Retained, autoreleasepool},
    runtime::AnyObject,
};
use objc2_core_foundation::CGSize;
use objc2_core_foundation::{CFRetained, CFType};
use objc2_core_graphics::CGImage;
use objc2_core_media::CMTime;
use objc2_foundation::{NSArray, NSNumber, NSString, NSURL};
use std::{
    ffi::c_void,
    path::Path,
    ptr::NonNull,
    sync::{Mutex, mpsc},
};
use wallpaper_core::{
    fit::MAX_IMAGE_PIXELS,
    library::{Kind, Probe, RejectCode, Rejection},
};

#[link(name = "ImageIO", kind = "framework")]
unsafe extern "C" {
    fn CGImageSourceGetCount(source: *const CFType) -> usize;
    fn CGImageDestinationCreateWithURL(
        url: *const c_void,
        kind: *const c_void,
        count: usize,
        options: *const c_void,
    ) -> *mut CFType;
    fn CGImageDestinationAddImage(dst: *const CFType, image: &CGImage, options: *const AnyObject);
    fn CGImageDestinationFinalize(dst: *const CFType) -> bool;
    static kCGImageDestinationLossyCompressionQuality: *const AnyObject;
}
#[link(name = "CoreGraphics", kind = "framework")]
unsafe extern "C" {
    fn CGColorSpaceCreateDeviceRGB() -> *mut CFType;
    fn CGBitmapContextCreate(
        data: *mut c_void,
        width: usize,
        height: usize,
        bits: usize,
        stride: usize,
        space: *const CFType,
        info: u32,
    ) -> *mut CFType;
    fn CGContextDrawImage(ctx: *const CFType, rect: objc2_core_foundation::CGRect, image: &CGImage);
    fn CGBitmapContextCreateImage(ctx: *const CFType) -> *mut CGImage;
}
fn failure(code: RejectCode, detail: impl ToString) -> Rejection {
    Rejection::new(code, detail)
}
pub(crate) fn probe(
    path: &Path,
    kind: Kind,
    thumbnail: Option<&Path>,
) -> std::result::Result<Probe, Rejection> {
    autoreleasepool(|_| {
        let (probe, image) = match kind {
            Kind::Image => image(path)?,
            Kind::Video => video(path)?,
        };
        if let Some(path) = thumbnail
            && let Err(e) = jpeg(&image.0, path)
        {
            eprintln!("thumbnail: {e:#}");
        }
        Ok(probe)
    })
}
fn image(path: &Path) -> std::result::Result<(Probe, Decoded), Rejection> {
    let result = (|| -> Result<_> {
        unsafe {
            let path = path.to_str().context("image path is not UTF-8")?;
            let url = NSURL::fileURLWithPath(&NSString::from_str(path));
            let source = CFRetained::from_raw(
                NonNull::new(CGImageSourceCreateWithURL(
                    (&*url as *const NSURL).cast(),
                    std::ptr::null(),
                ))
                .context("ImageIO could not open source")?,
            );
            if CGImageSourceGetCount(&*source) > 1 {
                return Ok(Err(failure(
                    RejectCode::AnimatedImage,
                    "v1 does not support animated images",
                )));
            }
            let props = CFRetained::from_raw(
                NonNull::new(CGImageSourceCopyPropertiesAtIndex(
                    &*source,
                    0,
                    std::ptr::null(),
                ))
                .context("missing image dimensions")?,
            );
            let dictionary = (&*props as *const CFType).cast::<AnyObject>();
            let number = |key: *const AnyObject| -> u32 {
                let value: *const AnyObject = msg_send![dictionary, objectForKey: key];
                if value.is_null() {
                    0
                } else {
                    let n: u64 = msg_send![value, unsignedLongLongValue];
                    u32::try_from(n).unwrap_or(u32::MAX)
                }
            };
            let mut width = number(kCGImagePropertyPixelWidth);
            let mut height = number(kCGImagePropertyPixelHeight);
            if u64::from(width) * u64::from(height) > MAX_IMAGE_PIXELS {
                return Ok(Err(failure(
                    RejectCode::TooLarge,
                    "image exceeds MAX_IMAGE_PIXELS",
                )));
            }
            if (5..=8).contains(&number(kCGImagePropertyOrientation)) {
                std::mem::swap(&mut width, &mut height);
            }
            anyhow::ensure!(width > 0 && height > 0, "invalid image dimensions");
            let long = width.max(height) as f64;
            let tw = (width as f64 * 480. / long).round().max(1.) as u32;
            let th = (height as f64 * 480. / long).round().max(1.) as u32;
            let decoded = decode(path, tw, th)?;
            Ok(Ok((
                Probe {
                    kind: Kind::Image,
                    width,
                    height,
                    duration_sec: None,
                },
                decoded,
            )))
        }
    })();
    result.map_err(|e| failure(RejectCode::DecodeFailed, e))?
}
fn jpeg(image: &CGImage, path: &Path) -> Result<()> {
    unsafe {
        let w = CGImage::width(Some(image));
        let h = CGImage::height(Some(image));
        let long = w.max(h) as f64;
        let width = (w as f64 * 480. / long).round().max(1.) as usize;
        let height = (h as f64 * 480. / long).round().max(1.) as usize;
        let space = CFRetained::from_raw(
            NonNull::new(CGColorSpaceCreateDeviceRGB()).context("RGB color space")?,
        );
        // Zeroed opaque RGBX bitmap composites alpha onto black.
        let ctx = CFRetained::from_raw(
            NonNull::new(CGBitmapContextCreate(
                std::ptr::null_mut(),
                width,
                height,
                8,
                width * 4,
                &*space,
                5,
            ))
            .context("thumbnail bitmap")?,
        );
        CGContextDrawImage(
            &*ctx,
            objc2_core_foundation::CGRect::new(
                objc2_core_foundation::CGPoint::new(0., 0.),
                CGSize::new(width as f64, height as f64),
            ),
            image,
        );
        let opaque = CFRetained::from_raw(
            NonNull::new(CGBitmapContextCreateImage(&*ctx)).context("opaque thumbnail")?,
        );
        let url = NSURL::fileURLWithPath(&NSString::from_str(
            path.to_str().context("thumbnail path is not UTF-8")?,
        ));
        let kind = NSString::from_str("public.jpeg");
        let dst = CFRetained::from_raw(
            NonNull::new(CGImageDestinationCreateWithURL(
                (&*url as *const NSURL).cast(),
                (&*kind as *const NSString).cast(),
                1,
                std::ptr::null(),
            ))
            .context("JPEG destination")?,
        );
        let options: Retained<AnyObject> = msg_send![class!(NSMutableDictionary), new];
        let quality = NSNumber::numberWithDouble(0.8);
        let _: () = msg_send![&*options, setObject: &*quality, forKey: kCGImageDestinationLossyCompressionQuality];
        CGImageDestinationAddImage(&*dst, &opaque, &*options);
        anyhow::ensure!(CGImageDestinationFinalize(&*dst), "JPEG write failed");
        Ok(())
    }
}

// Global Objective-C blocks have no captures and require no copy/dispose helpers.
// A gate serializes this process-local callback bridge. Only immutable CGImage
// references and completion signals cross the callback's arbitrary queue.
#[repr(C)]
struct Block {
    isa: *const c_void,
    flags: i32,
    reserved: i32,
    invoke: *const c_void,
    descriptor: *const Descriptor,
}
#[repr(C)]
struct Descriptor {
    reserved: usize,
    size: usize,
}
unsafe impl Sync for Block {}
unsafe impl objc2::encode::RefEncode for Block {
    const ENCODING_REF: objc2::encode::Encoding = objc2::encode::Encoding::Block;
}
unsafe extern "C" {
    static _NSConcreteGlobalBlock: c_void;
}
static DESCRIPTOR: Descriptor = Descriptor {
    reserved: 0,
    size: std::mem::size_of::<Block>(),
};
static LOADED: Mutex<Option<mpsc::Sender<()>>> = Mutex::new(None);
static FRAME: Mutex<Option<mpsc::Sender<Result<Decoded, String>>>> = Mutex::new(None);
static VIDEO_GATE: Mutex<()> = Mutex::new(());
extern "C" fn loaded(_: *const Block) {
    if let Ok(sender) = LOADED.lock()
        && let Some(sender) = &*sender
    {
        let _ = sender.send(());
    }
}
extern "C" fn generated(
    _: *const Block,
    _: CMTime,
    image: *mut CGImage,
    _: CMTime,
    result: isize,
    error: *mut AnyObject,
) {
    let image = autoreleasepool(|_| {
        if result == 0 {
            NonNull::new(image)
                .map(|p| Decoded(unsafe { CFRetained::retain(p) }))
                .ok_or_else(|| "system decoder returned a null frame".into())
        } else {
            let detail = if error.is_null() {
                format!("system decoder could not decode first video frame (result {result})")
            } else {
                let description: Retained<NSString> = unsafe { msg_send![error, description] };
                description.to_string()
            };
            Err(detail)
        }
    });
    if let Ok(sender) = FRAME.lock()
        && let Some(sender) = &*sender
    {
        let _ = sender.send(image);
    }
}
static LOAD_BLOCK: Block = Block {
    isa: &raw const _NSConcreteGlobalBlock,
    flags: 1 << 28,
    reserved: 0,
    invoke: loaded as *const c_void,
    descriptor: &DESCRIPTOR,
};
static FRAME_BLOCK: Block = Block {
    isa: &raw const _NSConcreteGlobalBlock,
    flags: 1 << 28,
    reserved: 0,
    invoke: generated as *const c_void,
    descriptor: &DESCRIPTOR,
};
fn video(path: &Path) -> std::result::Result<(Probe, Decoded), Rejection> {
    let _gate = VIDEO_GATE
        .lock()
        .map_err(|e| failure(RejectCode::DecodeFailed, e))?;
    let result = (|| -> Result<_> {
        unsafe {
            let url = NSURL::fileURLWithPath(&NSString::from_str(
                path.to_str().context("video path is not UTF-8")?,
            ));
            let asset: Retained<AnyObject> = msg_send![class!(AVURLAsset), URLAssetWithURL: &*url, options: std::ptr::null::<AnyObject>()];
            let keys = NSArray::from_retained_slice(&[
                NSString::from_str("tracks"),
                NSString::from_str("duration"),
            ]);
            let (tx, rx) = mpsc::channel();
            *LOADED.lock().unwrap() = Some(tx);
            let _: () = msg_send![&*asset, loadValuesAsynchronouslyForKeys: &*keys, completionHandler: &LOAD_BLOCK as *const Block];
            rx.recv().context("asset loading callback disconnected")?;
            *LOADED.lock().unwrap() = None;
            let media_type = NSString::from_str("vide");
            let tracks: Retained<NSArray<AnyObject>> =
                msg_send![&*asset, tracksWithMediaType: &*media_type];
            if tracks.count() == 0 {
                return Ok(Err(failure(
                    RejectCode::NoVideoTrack,
                    "asset has no video track",
                )));
            }
            let track = tracks.objectAtIndex(0);
            let size: CGSize = msg_send![&*track, naturalSize];
            let transform: objc2_core_foundation::CGAffineTransform =
                msg_send![&*track, preferredTransform];
            let width = (size.width * transform.a).abs() + (size.height * transform.c).abs();
            let height = (size.width * transform.b).abs() + (size.height * transform.d).abs();
            anyhow::ensure!(
                width.is_finite() && height.is_finite() && width >= 1. && height >= 1.,
                "invalid video dimensions"
            );
            let duration: CMTime = msg_send![&*asset, duration];
            let seconds = duration.seconds();
            let generator: Retained<AnyObject> =
                msg_send![class!(AVAssetImageGenerator), assetImageGeneratorWithAsset: &*asset];
            let _: () = msg_send![&*generator, setAppliesPreferredTrackTransform: true];
            let _: () = msg_send![&*generator, setMaximumSize: CGSize::new(480.,480.)];
            let time: Retained<AnyObject> =
                msg_send![class!(NSValue), valueWithCMTime: CMTime::new(0, 1)];
            let times = NSArray::from_retained_slice(&[time]);
            let (tx, rx) = mpsc::channel();
            *FRAME.lock().unwrap() = Some(tx);
            let _: () = msg_send![&*generator, generateCGImagesAsynchronouslyForTimes: &*times, completionHandler: &FRAME_BLOCK as *const Block];
            let frame = rx
                .recv()
                .context("frame callback disconnected")?
                .map_err(|detail| anyhow::anyhow!(detail))?;
            *FRAME.lock().unwrap() = None;
            Ok(Ok((
                Probe {
                    kind: Kind::Video,
                    width: width.round() as u32,
                    height: height.round() as u32,
                    duration_sec: (seconds.is_finite() && seconds >= 0.).then_some(seconds),
                },
                frame,
            )))
        }
    })();
    result.map_err(|e| failure(RejectCode::DecodeFailed, e))?
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::{
        path::PathBuf,
        sync::atomic::{AtomicU64, Ordering},
    };
    struct Fixture(PathBuf);
    impl Fixture {
        fn new() -> Self {
            static SERIAL: AtomicU64 = AtomicU64::new(0);
            let path = std::env::temp_dir().join(format!(
                "wp-probe-{}-{}",
                std::process::id(),
                SERIAL.fetch_add(1, Ordering::Relaxed)
            ));
            std::fs::create_dir(&path).unwrap();
            Self(path)
        }
    }
    impl Drop for Fixture {
        fn drop(&mut self) {
            let _ = std::fs::remove_dir_all(&self.0);
        }
    }
    #[test]
    fn real_image_decode_orientation_jpeg_and_write_failure() {
        let f = Fixture::new();
        for orientation in [1, 5, 6, 7, 8] {
            let path = f.0.join("still.tiff");
            std::fs::write(
                &path,
                super::super::image::tests::tiff(20, 80, orientation, true),
            )
            .unwrap();
            let thumb = f.0.join("thumb.jpg");
            let result = probe(&path, Kind::Image, Some(&thumb)).unwrap();
            assert_eq!(
                (result.width, result.height),
                if orientation == 1 { (20, 80) } else { (80, 20) }
            );
            let jpeg = probe(&thumb, Kind::Image, None).unwrap();
            assert_eq!(
                (jpeg.width, jpeg.height),
                if orientation == 1 {
                    (120, 480)
                } else {
                    (480, 120)
                }
            );
            assert!(probe(&path, Kind::Image, Some(&f.0.join("missing/thumb.jpg"))).is_ok());
        }
    }
    #[test]
    fn generated_png_transparency_is_composited_on_black() {
        let f = Fixture::new();
        let path = f.0.join("transparent.png");
        autoreleasepool(|_| unsafe {
            let space = CFRetained::from_raw(NonNull::new(CGColorSpaceCreateDeviceRGB()).unwrap());
            let mut pixels = [0u8, 0, 0, 0, 255, 0, 0, 255];
            let context = CFRetained::from_raw(
                NonNull::new(CGBitmapContextCreate(
                    pixels.as_mut_ptr().cast(),
                    2,
                    1,
                    8,
                    8,
                    &*space,
                    1,
                ))
                .unwrap(),
            );
            let image =
                CFRetained::from_raw(NonNull::new(CGBitmapContextCreateImage(&*context)).unwrap());
            let url = NSURL::fileURLWithPath(&NSString::from_str(path.to_str().unwrap()));
            let kind = NSString::from_str("public.png");
            let destination = CFRetained::from_raw(
                NonNull::new(CGImageDestinationCreateWithURL(
                    (&*url as *const NSURL).cast(),
                    (&*kind as *const NSString).cast(),
                    1,
                    std::ptr::null(),
                ))
                .unwrap(),
            );
            CGImageDestinationAddImage(&*destination, &image, std::ptr::null());
            assert!(CGImageDestinationFinalize(&*destination));
        });
        let thumb = f.0.join("thumb.jpg");
        let result = probe(&path, Kind::Image, Some(&thumb)).unwrap();
        assert_eq!((result.width, result.height), (2, 1));
        let (_, decoded) = image(&thumb).unwrap();
        autoreleasepool(|_| unsafe {
            let space = CFRetained::from_raw(NonNull::new(CGColorSpaceCreateDeviceRGB()).unwrap());
            let mut pixels = vec![0u8; 480 * 240 * 4];
            let context = CFRetained::from_raw(
                NonNull::new(CGBitmapContextCreate(
                    pixels.as_mut_ptr().cast(),
                    480,
                    240,
                    8,
                    480 * 4,
                    &*space,
                    5,
                ))
                .unwrap(),
            );
            CGContextDrawImage(
                &*context,
                objc2_core_foundation::CGRect::new(
                    objc2_core_foundation::CGPoint::new(0., 0.),
                    CGSize::new(480., 240.),
                ),
                &decoded.0,
            );
            assert!(
                pixels[..3].iter().all(|v| *v < 5),
                "transparent region was not black: {:?}",
                &pixels[..4]
            );
        });
    }
    #[test]
    fn real_multiframe_rejection_and_pixel_limit_and_corrupt_data() {
        let f = Fixture::new();
        let path = f.0.join("source.tiff");
        let mut tiff = super::super::image::tests::tiff(20, 80, 1, true);
        // Second IFD shares the first frame's valid pixel data.
        let second_ifd = tiff.len() as u32;
        tiff[130..134].copy_from_slice(&second_ifd.to_le_bytes());
        let mut ifd = tiff[8..134].to_vec();
        ifd[122..126].fill(0);
        tiff.extend_from_slice(&ifd);
        std::fs::write(&path, tiff).unwrap();
        assert_eq!(
            probe(&path, Kind::Image, None).unwrap_err().code,
            RejectCode::AnimatedImage
        );
        std::fs::write(
            &path,
            super::super::image::tests::tiff(20_001, 10_000, 1, false),
        )
        .unwrap();
        std::fs::OpenOptions::new()
            .write(true)
            .open(&path)
            .unwrap()
            .set_len(140 + 20_001 * 10_000 * 3)
            .unwrap();
        assert_eq!(
            probe(&path, Kind::Image, None).unwrap_err().code,
            RejectCode::TooLarge
        );
        std::fs::write(&path, b"not media").unwrap();
        assert_eq!(
            probe(&path, Kind::Image, None).unwrap_err().code,
            RejectCode::DecodeFailed
        );
    }
}
