use super::{image, wide};
use anyhow::{Context, Result, ensure};
use std::path::Path;
use wallpaper_core::{
    fit::MAX_IMAGE_PIXELS,
    library::{Fit, Kind, Probe, RejectCode, Rejection},
};
use windows::{
    Win32::{
        Foundation::{GENERIC_READ, GENERIC_WRITE},
        Graphics::Imaging::*,
        Media::MediaFoundation::*,
        System::Com::{StructuredStorage::*, *},
        System::Variant::VARIANT,
    },
    core::{PCWSTR, PWSTR, w},
};

struct Com;
impl Drop for Com {
    fn drop(&mut self) {
        unsafe {
            CoUninitialize();
        }
    }
}
struct Mf;
impl Drop for Mf {
    fn drop(&mut self) {
        unsafe {
            let _ = MFShutdown();
        }
    }
}
fn failure(e: impl ToString) -> Rejection {
    Rejection::new(RejectCode::DecodeFailed, e)
}
pub(crate) fn probe(
    path: &Path,
    kind: Kind,
    thumbnail: Option<&Path>,
) -> std::result::Result<Probe, Rejection> {
    unsafe {
        CoInitializeEx(None, COINIT_MULTITHREADED)
            .ok()
            .map_err(failure)?;
    }
    let _com = Com;
    let path = path
        .to_str()
        .ok_or_else(|| failure("media path is not UTF-8"))?;
    let factory: IWICImagingFactory = unsafe {
        CoCreateInstance(&CLSID_WICImagingFactory, None, CLSCTX_INPROC_SERVER).map_err(failure)?
    };
    let (probe, bitmap) = match kind {
        Kind::Image => still(&factory, path)?,
        Kind::Video => {
            unsafe {
                MFStartup(MF_VERSION, MFSTARTUP_FULL).map_err(failure)?;
            }
            let _mf = Mf;
            video(&factory, path)?
        }
    };
    if let Some(path) = thumbnail
        && let Err(e) = jpeg(&factory, &bitmap, path)
    {
        eprintln!("thumbnail: {e:#}");
    }
    Ok(probe)
}
fn still(
    factory: &IWICImagingFactory,
    path: &str,
) -> std::result::Result<(Probe, IWICBitmap), Rejection> {
    let result = (|| -> Result<_> {
        unsafe {
            let encoded = wide(path);
            let decoder = factory.CreateDecoderFromFilename(
                PCWSTR(encoded.as_ptr()),
                None,
                GENERIC_READ,
                WICDecodeMetadataCacheOnDemand,
            )?;
            if decoder.GetFrameCount()? > 1 {
                return Ok(Err(Rejection::new(
                    RejectCode::AnimatedImage,
                    "v1 does not support animated images",
                )));
            }
            let frame = decoder.GetFrame(0)?;
            let (mut width, mut height) = (0, 0);
            frame.GetSize(&mut width, &mut height)?;
            if u64::from(width) * u64::from(height) > MAX_IMAGE_PIXELS {
                return Ok(Err(Rejection::new(
                    RejectCode::TooLarge,
                    "image exceeds MAX_IMAGE_PIXELS",
                )));
            }
            ensure!(width > 0 && height > 0, "invalid image dimensions");
            if (5..=8).contains(&image::orientation(&frame)) {
                std::mem::swap(&mut width, &mut height);
            }
            let (tw, th) = thumb_size(width, height);
            let pixels = image::decode(path, tw, th, Fit::Stretch)?;
            let bitmap = factory.CreateBitmapFromMemory(
                tw,
                th,
                &GUID_WICPixelFormat32bppBGR,
                tw * 4,
                &pixels,
            )?;
            Ok(Ok((
                Probe {
                    kind: Kind::Image,
                    width,
                    height,
                    duration_sec: None,
                },
                bitmap,
            )))
        }
    })();
    result.map_err(failure)?
}
fn thumb_size(width: u32, height: u32) -> (u32, u32) {
    let long = width.max(height) as f64;
    (
        (width as f64 * 480. / long).round().max(1.) as u32,
        (height as f64 * 480. / long).round().max(1.) as u32,
    )
}
fn video(
    factory: &IWICImagingFactory,
    path: &str,
) -> std::result::Result<(Probe, IWICBitmap), Rejection> {
    let result = (|| -> Result<_> {
        unsafe {
            let mut attributes = None;
            MFCreateAttributes(&mut attributes, 1)?;
            let attributes = attributes.context("source reader attributes")?;
            attributes.SetUINT32(&MF_SOURCE_READER_ENABLE_VIDEO_PROCESSING, 1)?;
            let encoded = wide(path);
            let reader = MFCreateSourceReaderFromURL(PCWSTR(encoded.as_ptr()), &attributes)?;
            let stream = MF_SOURCE_READER_FIRST_VIDEO_STREAM.0 as u32;
            // Inspect a native video type first; distinguish missing tracks from decode errors.
            let native = match reader.GetNativeMediaType(stream, 0) {
                Ok(native) => native,
                Err(e) if e.code() == MF_E_INVALIDSTREAMNUMBER => {
                    return Ok(Err(Rejection::new(
                        RejectCode::NoVideoTrack,
                        "asset has no video track",
                    )));
                }
                Err(e) => return Err(e.into()),
            };
            let rotation = native.GetUINT32(&MF_MT_VIDEO_ROTATION).unwrap_or(0);
            reader.SetStreamSelection(MF_SOURCE_READER_ALL_STREAMS.0 as u32, false)?;
            reader.SetStreamSelection(stream, true)?;
            let media_type = MFCreateMediaType()?;
            media_type.SetGUID(&MF_MT_MAJOR_TYPE, &MFMediaType_Video)?;
            media_type.SetGUID(&MF_MT_SUBTYPE, &MFVideoFormat_RGB32)?;
            reader.SetCurrentMediaType(stream, None, &media_type)?;
            let duration_sec = reader
                .GetPresentationAttribute(MF_SOURCE_READER_MEDIASOURCE.0 as u32, &MF_PD_DURATION)
                .ok()
                .and_then(|mut value| {
                    let duration = PropVariantToUInt64(&value)
                        .ok()
                        .map(|ticks| ticks as f64 / 10_000_000.);
                    let _ = PropVariantClear(&mut value);
                    duration
                });
            for _ in 0..120 {
                let mut flags = 0;
                let mut sample = None;
                reader.ReadSample(stream, 0, None, Some(&mut flags), None, Some(&mut sample))?;
                ensure!(
                    flags & MF_SOURCE_READERF_ERROR.0 as u32 == 0,
                    "source reader decode error"
                );
                if let Some(sample) = sample {
                    let current = reader.GetCurrentMediaType(stream)?;
                    let dimensions = current.GetUINT64(&MF_MT_FRAME_SIZE)?;
                    let width = (dimensions >> 32) as u32;
                    let height = dimensions as u32;
                    ensure!(width > 0 && height > 0, "invalid video dimensions");
                    let row = width.checked_mul(4).context("video row overflow")?;
                    let stride = match current.GetUINT32(&MF_MT_DEFAULT_STRIDE) {
                        Ok(stride) => stride as i32,
                        Err(_) => MFGetStrideForBitmapInfoHeader(
                            current.GetGUID(&MF_MT_SUBTYPE)?.data1,
                            width,
                        )?,
                    };
                    let pitch = stride.unsigned_abs();
                    ensure!(pitch >= row, "invalid video stride");
                    let buffer = sample.ConvertToContiguousBuffer()?;
                    let mut data = std::ptr::null_mut();
                    let mut length = 0;
                    buffer.Lock(&mut data, None, Some(&mut length))?;
                    let copied = (|| -> Result<Vec<u8>> {
                        let required = u64::from(pitch) * u64::from(height - 1) + u64::from(row);
                        ensure!(
                            required <= u64::from(length) && !data.is_null(),
                            "truncated video buffer"
                        );
                        let size = (row as usize)
                            .checked_mul(height as usize)
                            .context("video buffer overflow")?;
                        let mut pixels = vec![0; size];
                        for y in 0..height as usize {
                            let source_y = if stride < 0 {
                                height as usize - 1 - y
                            } else {
                                y
                            };
                            let source = std::slice::from_raw_parts(
                                data.add(source_y * pitch as usize),
                                row as usize,
                            );
                            pixels[y * row as usize..(y + 1) * row as usize]
                                .copy_from_slice(source);
                        }
                        Ok(pixels)
                    })();
                    buffer.Unlock()?;
                    let pixels = copied?;
                    let bitmap = factory.CreateBitmapFromMemory(
                        width,
                        height,
                        &GUID_WICPixelFormat32bppBGR,
                        row,
                        &pixels,
                    )?;
                    let rotator = factory.CreateBitmapFlipRotator()?;
                    rotator.Initialize(
                        &bitmap,
                        match rotation {
                            90 => WICBitmapTransformRotate90,
                            180 => WICBitmapTransformRotate180,
                            270 => WICBitmapTransformRotate270,
                            _ => WICBitmapTransformRotate0,
                        },
                    )?;
                    let (width, height) = if rotation == 90 || rotation == 270 {
                        (height, width)
                    } else {
                        (width, height)
                    };
                    let (tw, th) = thumb_size(width, height);
                    let scaler = factory.CreateBitmapScaler()?;
                    scaler.Initialize(&rotator, tw, th, WICBitmapInterpolationModeFant)?;
                    let bitmap = factory.CreateBitmapFromSource(&scaler, WICBitmapCacheOnLoad)?;
                    return Ok(Ok((
                        Probe {
                            kind: Kind::Video,
                            width,
                            height,
                            duration_sec,
                        },
                        bitmap,
                    )));
                }
                ensure!(
                    flags & MF_SOURCE_READERF_ENDOFSTREAM.0 as u32 == 0,
                    "EOF before first decoded frame"
                );
                // Empty samples, including stream ticks, continue within the bound.
            }
            anyhow::bail!("no decoded frame after 120 samples")
        }
    })();
    result.map_err(failure)?
}
fn jpeg(factory: &IWICImagingFactory, bitmap: &IWICBitmap, path: &Path) -> Result<()> {
    unsafe {
        let stream = factory.CreateStream()?;
        let path = wide(path.to_str().context("thumbnail path is not UTF-8")?);
        stream.InitializeFromFilename(PCWSTR(path.as_ptr()), GENERIC_WRITE.0)?;
        let encoder = factory.CreateEncoder(&GUID_ContainerFormatJpeg, std::ptr::null())?;
        encoder.Initialize(&stream, WICBitmapEncoderNoCache)?;
        let (mut frame, mut properties) = (None, None);
        encoder.CreateNewFrame(&mut frame, &mut properties)?;
        let frame = frame.context("JPEG frame")?;
        if let Some(properties) = &properties {
            let prop = PROPBAG2 {
                pstrName: PWSTR(w!("ImageQuality").as_ptr().cast_mut()),
                ..Default::default()
            };
            properties.Write(1, &prop, &VARIANT::from(0.8f32))?;
        }
        frame.Initialize(properties.as_ref())?;
        let (mut width, mut height) = (0, 0);
        bitmap.GetSize(&mut width, &mut height)?;
        frame.SetSize(width, height)?;
        let mut format = GUID_WICPixelFormat24bppBGR;
        frame.SetPixelFormat(&mut format)?;
        let converter = factory.CreateFormatConverter()?;
        converter.Initialize(
            bitmap,
            &format,
            WICBitmapDitherTypeNone,
            None,
            0.,
            WICBitmapPaletteTypeCustom,
        )?;
        frame.WriteSource(&converter, std::ptr::null())?;
        frame.Commit()?;
        encoder.Commit()?;
        Ok(())
    }
}
