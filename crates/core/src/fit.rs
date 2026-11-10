use crate::library::Fit;

pub const MAX_IMAGE_PIXELS: u64 = 200_000_000;

#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Placement {
    pub source: [f32; 4],
    pub dest: [u32; 4],
}

pub fn place(src_w: u32, src_h: u32, dst_w: u32, dst_h: u32, fit: Fit) -> Option<Placement> {
    if [src_w, src_h, dst_w, dst_h].contains(&0) {
        return None;
    }
    let mut result = Placement {
        source: [0., 0., 1., 1.],
        dest: [0, 0, dst_w, dst_h],
    };
    let source = u64::from(src_w) * u64::from(dst_h);
    let target = u64::from(dst_w) * u64::from(src_h);
    match fit {
        Fit::Fill => {
            let (axis, fraction) = if source > target {
                (0, target as f64 / source as f64)
            } else {
                (1, source as f64 / target as f64)
            };
            result.source[axis] = ((1. - fraction) / 2.) as f32;
            result.source[axis + 2] = ((1. + fraction) / 2.) as f32;
        }
        Fit::Fit => {
            // Integer arithmetic avoids overflow and guarantees an in-bounds,
            // nonempty destination even for extreme aspect ratios.
            let (width, height) = if source > target {
                (
                    dst_w,
                    (u64::from(src_h) * u64::from(dst_w) / u64::from(src_w)).max(1) as u32,
                )
            } else {
                (
                    (u64::from(src_w) * u64::from(dst_h) / u64::from(src_h)).max(1) as u32,
                    dst_h,
                )
            };
            let left = (dst_w - width) / 2;
            let top = (dst_h - height) / 2;
            result.dest = [left, top, left + width, top + height];
        }
        Fit::Stretch => {}
    }
    Some(result)
}

pub fn decode_long_side(src_w: u32, src_h: u32, dst_w: u32, dst_h: u32) -> u32 {
    if [src_w, src_h, dst_w, dst_h].contains(&0) {
        return 0;
    }
    let long = u64::from(src_w.max(src_h));
    let width = (long * u64::from(dst_w)).div_ceil(u64::from(src_w));
    let height = (long * u64::from(dst_h)).div_ceil(u64::from(src_h));
    width.max(height).min(long) as u32
}

pub fn validate_image_size(width: u32, height: u32) -> Result<(), String> {
    let pixels = u64::from(width) * u64::from(height);
    if pixels == 0 {
        Err(format!(
            "image dimensions must be nonzero: {width}x{height}"
        ))
    } else if pixels > MAX_IMAGE_PIXELS {
        Err(format!(
            "image has {pixels} pixels ({width}x{height}), exceeding MAX_IMAGE_PIXELS={MAX_IMAGE_PIXELS}"
        ))
    } else {
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn equal_aspect_and_one_pixel() {
        for fit in [Fit::Fill, Fit::Fit, Fit::Stretch] {
            assert_eq!(
                place(200, 100, 100, 50, fit),
                Some(Placement {
                    source: [0., 0., 1., 1.],
                    dest: [0, 0, 100, 50]
                })
            );
            assert_eq!(
                place(1, 1, 1, 1, fit),
                Some(Placement {
                    source: [0., 0., 1., 1.],
                    dest: [0, 0, 1, 1]
                })
            );
        }
    }
    #[test]
    fn landscape_on_portrait_and_portrait_on_landscape() {
        assert_eq!(
            place(200, 100, 100, 200, Fit::Fill).unwrap().source,
            [0.375, 0., 0.625, 1.]
        );
        assert_eq!(
            place(100, 200, 200, 100, Fit::Fill).unwrap().source,
            [0., 0.375, 1., 0.625]
        );
        assert_eq!(
            place(200, 100, 100, 200, Fit::Fit).unwrap().dest,
            [0, 75, 100, 125]
        );
        assert_eq!(
            place(100, 200, 200, 100, Fit::Fit).unwrap().dest,
            [75, 0, 125, 100]
        );
        assert_eq!(
            place(100, 200, 200, 100, Fit::Stretch).unwrap(),
            Placement {
                source: [0., 0., 1., 1.],
                dest: [0, 0, 200, 100]
            }
        );
    }
    #[test]
    fn zero_dimensions() {
        for dims in [[0, 1, 1, 1], [1, 0, 1, 1], [1, 1, 0, 1], [1, 1, 1, 0]] {
            let [a, b, c, d] = dims;
            for fit in [Fit::Fill, Fit::Fit, Fit::Stretch] {
                assert!(place(a, b, c, d, fit).is_none());
            }
            assert_eq!(decode_long_side(a, b, c, d), 0);
        }
    }
    #[test]
    fn extreme_ratios_and_dest_bounds() {
        for w in [1, 3, 1080, u32::MAX] {
            for h in [1, 7, 1920, u32::MAX] {
                for (dw, dh) in [(1, 1), (1920, 1080), (3, 7), (u32::MAX, 1), (1, u32::MAX)] {
                    for fit in [Fit::Fill, Fit::Fit, Fit::Stretch] {
                        let p = place(w, h, dw, dh, fit).unwrap();
                        assert!(p.source.iter().all(|v| (0. ..=1.).contains(v)));
                        assert!(p.source[0] <= p.source[2] && p.source[1] <= p.source[3]);
                        assert!(p.dest[0] < p.dest[2] && p.dest[2] <= dw);
                        assert!(p.dest[1] < p.dest[3] && p.dest[3] <= dh);
                        if fit != Fit::Fit {
                            assert_eq!(p.dest, [0, 0, dw, dh]);
                        }
                    }
                }
            }
        }
    }
    #[test]
    fn decode_portrait_fill_needs_more_than_screen_long_side() {
        assert_eq!(decode_long_side(2000, 8000, 1920, 1080), 7680);
        assert_eq!(decode_long_side(1000, 8000, 1920, 1080), 8000);
        assert_eq!(decode_long_side(6000, 4000, 1920, 1080), 1920);
        assert_eq!(decode_long_side(3, 7, 2, 2), 5);
        assert_eq!(decode_long_side(u32::MAX, 1, u32::MAX, u32::MAX), u32::MAX);
        assert_eq!(decode_long_side(1, 1, 100, 100), 1);
    }
    #[test]
    fn image_pixel_limit_and_overflow() {
        assert!(validate_image_size(20_000, 10_000).is_ok());
        assert!(
            validate_image_size(20_001, 10_000)
                .unwrap_err()
                .contains("exceeding MAX_IMAGE_PIXELS=200000000")
        );
        assert!(validate_image_size(u32::MAX, u32::MAX).is_err());
        assert!(validate_image_size(0, 1).unwrap_err().contains("nonzero"));
    }
}
