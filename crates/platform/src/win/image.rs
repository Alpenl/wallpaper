use super::{wide, worker::RenderTarget};
use anyhow::{Context, Result, anyhow};
use wallpaper_core::{
    Evidence, Truth,
    fit::{place, validate_image_size},
    library::Fit,
};
use windows::{
    Win32::{
        Foundation::GENERIC_READ,
        Graphics::{Direct3D11::*, Dxgi::DXGI_PRESENT, Imaging::*},
        System::Com::{StructuredStorage::*, *},
    },
    core::{PCWSTR, w},
};

pub(super) struct Image {
    // Keep the swap chain and its last presented buffer alive until Stop/exit.
    _render: RenderTarget,
    pub evidence: Evidence,
}
impl Image {
    pub fn new(path: &str, window: usize, width: u32, height: u32, fit: Fit) -> Result<Self> {
        let render = RenderTarget::new(window, width, height)?;
        let pixels = decode(path, width, height, fit)?;
        unsafe {
            let mut description = D3D11_TEXTURE2D_DESC::default();
            render.texture.GetDesc(&mut description);
            description.Usage = D3D11_USAGE_DEFAULT;
            description.BindFlags = 0;
            description.CPUAccessFlags = 0;
            description.MiscFlags = 0;
            let data = D3D11_SUBRESOURCE_DATA {
                pSysMem: pixels.as_ptr().cast(),
                SysMemPitch: width * 4,
                SysMemSlicePitch: 0,
            };
            let mut upload = None;
            render
                .device
                .CreateTexture2D(&description, Some(&data), Some(&mut upload))?;
            render.device.GetImmediateContext()?.CopyResource(
                &render.texture,
                &upload.context("image: no upload texture")?,
            );
            render.swap.Present(0, DXGI_PRESENT(0)).ok()?;
        }
        Ok(Self {
            _render: render,
            evidence: Evidence {
                ready_for_display: Truth::Yes,
                presented_frames: Some(1),
                position: None,
                child_pid: std::process::id(),
                ..Evidence::default()
            },
        })
    }
}

pub(super) fn orientation(frame: &IWICBitmapFrameDecode) -> u32 {
    unsafe {
        let Ok(reader) = frame.GetMetadataQueryReader() else {
            return 1;
        };
        let mut value = PROPVARIANT::default();
        let orientation = reader
            .GetMetadataByName(w!("/app1/ifd/{ushort=274}"), &mut value)
            .and_then(|()| PropVariantToUInt32(&value))
            .unwrap_or(1);
        let _ = PropVariantClear(&mut value);
        orientation
    }
}

pub(super) fn decode(path: &str, width: u32, height: u32, fit: Fit) -> Result<Vec<u8>> {
    unsafe {
        let factory: IWICImagingFactory =
            CoCreateInstance(&CLSID_WICImagingFactory, None, CLSCTX_INPROC_SERVER)?;
        let path = wide(path);
        let decoder = factory.CreateDecoderFromFilename(
            PCWSTR(path.as_ptr()),
            None,
            GENERIC_READ,
            WICDecodeMetadataCacheOnDemand,
        )?;
        let frame = decoder.GetFrame(0)?;
        let orientation = orientation(&frame);
        // Apply EXIF reflection and rotation separately: do not depend on WIC's
        // ordering of combined flip/rotate flags for transpose orientations.
        let reflection = factory.CreateBitmapFlipRotator()?;
        reflection.Initialize(
            &frame,
            match orientation {
                2 | 5 | 7 => WICBitmapTransformFlipHorizontal,
                4 => WICBitmapTransformFlipVertical,
                _ => WICBitmapTransformRotate0,
            },
        )?;
        let rotator = factory.CreateBitmapFlipRotator()?;
        rotator.Initialize(
            &reflection,
            match orientation {
                3 => WICBitmapTransformRotate180,
                5 | 8 => WICBitmapTransformRotate270,
                6 | 7 => WICBitmapTransformRotate90,
                _ => WICBitmapTransformRotate0,
            },
        )?;
        let mut iw = 0;
        let mut ih = 0;
        rotator.GetSize(&mut iw, &mut ih)?;
        validate_image_size(iw, ih).map_err(|e| anyhow!(e))?;
        let converter = factory.CreateFormatConverter()?;
        converter.Initialize(
            &rotator,
            &GUID_WICPixelFormat32bppPBGRA,
            WICBitmapDitherTypeNone,
            None,
            0.,
            WICBitmapPaletteTypeCustom,
        )?;
        let placement = place(iw, ih, width, height, fit).context("image: invalid placement")?;
        let [left, top, right, bottom] = placement.source;
        // Round crop edges to source pixels; retain at least one pixel even
        // when the normalized f32 edges collapse at extreme aspect ratios.
        let x = (f64::from(left) * f64::from(iw))
            .round()
            .min(f64::from(iw - 1)) as u32;
        let y = (f64::from(top) * f64::from(ih))
            .round()
            .min(f64::from(ih - 1)) as u32;
        let right = ((f64::from(right) * f64::from(iw)).round() as u32).clamp(x + 1, iw);
        let bottom = ((f64::from(bottom) * f64::from(ih)).round() as u32).clamp(y + 1, ih);
        let crop = WICRect {
            X: x as i32,
            Y: y as i32,
            Width: (right - x) as i32,
            Height: (bottom - y) as i32,
        };
        let clipper = factory.CreateBitmapClipper()?;
        clipper.Initialize(&converter, &crop)?;
        let [left, top, right, bottom] = placement.dest;
        let scaler = factory.CreateBitmapScaler()?;
        scaler.Initialize(
            &clipper,
            right - left,
            bottom - top,
            WICBitmapInterpolationModeFant,
        )?;
        let stride = width.checked_mul(4).context("image: stride overflow")?;
        let bytes = (stride as usize)
            .checked_mul(height as usize)
            .context("image: buffer size overflow")?;
        let mut pixels = vec![0u8; bytes];
        let offset = top as usize * stride as usize + left as usize * 4;
        let length = (bottom - top - 1) as usize * stride as usize + (right - left) as usize * 4;
        scaler.CopyPixels(
            std::ptr::null(),
            stride,
            &mut pixels[offset..offset + length],
        )?;
        // PBGRA is already multiplied by alpha: retaining RGB and setting alpha
        // to opaque composites transparency over the initialized black canvas.
        for pixel in pixels.as_chunks_mut::<4>().0 {
            pixel[3] = 255;
        }
        Ok(pixels)
    }
}
