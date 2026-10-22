# Media conversion routes

This package never downloads or bundles an encoder. The application remains a native Go/MyGo program; FFmpeg, when explicitly selected or discovered, is a separate user-installed command-line tool, not a UI/runtime dependency.

## Selection and capability reporting

The application supplies its own absolute executable path to `EncoderForTargetWithHelper` and `CapabilitiesForWithHelper`. Selection order is:

1. A configured FFmpeg executable. An invalid configured path returns unavailable; it is never ignored.
2. On macOS, the supplied application executable running `-helper transcode`. The application supplies it only if `nativeav.Available()` can load the necessary platform framework symbols/classes.
3. A user-installed FFmpeg found in PATH.
4. On macOS only, `/usr/bin/avconvert` as a clearly labeled limited fallback.

`Available` means a route/executable is present, not that a specific codec or GPU has passed a runtime test. Strict routes reject an invalid output. The native route supports GIF and AVFoundation-decodable MP4/MOV; unsupported codecs, protected content, WebM and unavailable encoders produce errors. An optional compatible FFmpeg build can be selected for unsupported native input. Failed conversions do not silently switch routes.

The FFmpeg codec listing is checked inside the background encoding job, never in UI rendering. macOS requires `h264_videotoolbox`; Windows requires `h264_mf`. Neither silently substitutes `libx264`. Linux retains the external `libx264` compatibility route. `-allow_sw 1` permits Apple's own VideoToolbox software implementation; selecting a system framework is not proof of hardware acceleration. Windows uses Media Foundation's `camera_record` scenario for CFR, GOP 30 and no B frames. x264-only CRF/preset flags are confined to the Linux software route.

External FFmpeg jobs are serialized and have a one-hour process deadline, two codec threads, one filter thread, a 256 MiB single-allocation cap and a 64 KiB retained diagnostic log. These are not a hard total-RSS or output-file-size limit; no new arbitrary source-duration or disk-size cutoff is imposed on the external route.

The `avconvert` fallback has `Strict`, `GIF`, `FPS30`, `TargetSize` and `RemovesAudio` all false. Its preset is not a substitute for fixed frame rate, size or audio removal. It rejects GIF explicitly and verifies basic playable H.264/HEVC output before publishing.

## Strict output contract

- H.264 High, MP4, video only, constant 30 fps, first sync sample and at most 30 frames between sync samples.
- Even pixel dimensions within `Target{Width, Height}`, aspect preserved with at most rounding error, no enlargement of smaller input.
- Default bound 3840×2160; target dimensions are capped at 8192 per side. The native route additionally bounds decoded/output canvas pixels.
- No source audio, subtitles, data or metadata tracks are copied.
- Output is written to a private sibling temporary file, inspected, and renamed only after success. Cancellation/failure preserves an existing destination and removes the partial file.

`VerifyOutput` checks the actual MP4 sample tables: one H.264 video track, no audio, dimensions, complete timing table/CFR, frame count/duration consistency and sync-sample spacing. This is not a full bitstream decoder. The cloud tests independently use ffprobe, decode the converted GIF, and inspect H.264 NAL units with FFmpeg's `trace_headers` filter to verify IDR output on the tested Linux software route.

Bounding-box sizing preserves the shared fallback for every fit mode. `CoverEncoder` additionally produces exact even-pixel center-cover renditions, with enlargement allowed. The app derives versioned keys from item/source revision, physical display size and crop rule, serializes these jobs with shared conversion/imports, verifies output through the isolated probe and `VerifyCover`, and publishes each MP4 with a manifest under `cache/<id>/renditions/<key>/`. Equal keys are reused. Non-fill, audible and spanning playback retain the shared/original source; unavailable or failed cover jobs never replace it. Cache cleanup pins desired, loaded and queued keys, while item deletion removes all variants.

## Native macOS helper

`nativeav.Encode(ctx, src, dst, width, height)` is invoked only by the isolated `wallpaperd -helper transcode -in SRC -out DST -width W -height H` child process. The UI process does not decode source frames. The destination must not already exist.

The native implementation uses a locked OS thread and autorelease pools, `AVAssetImageGenerator` for frame-accurate video images and orientation, and `AVAssetWriterInputPixelBufferAdaptor` for a single H.264 track. GOP/frame-rate settings are explicit. BGRA pixel buffers are allocated from a bounded pool after `startSessionAtSourceTime:`; end-session time is exactly frameCount/30. The per-frame image-generator approach prioritizes correctness over throughput and can be slow on long/high-resolution inputs. It is not a zero-copy AVAssetReader pipeline.

GIF decoding is pure Go inside the helper. A structural preflight limits compressed bytes, total decoded frame pixels, canvas size, frame count and duration before `image/gif.DecodeAll`; the compositor preserves subrectangles, transparency, frame delays and disposal-to-background/previous. The GIF loop count is ignored: one cycle is encoded and the player loops the video. Positive GIF delays are preserved; zero-delay native GIF frames use 100 ms. Non-square GIF pixel aspect and plain-text rendering are explicitly unsupported instead of being silently distorted.

Native safety limits include 2 GiB input, 30 minutes/54,000 output frames, 64 MiB compressed GIF, 128 MiB aggregate paletted frame pixels and 16 MiPixels canvas/output. Readiness and finalization waits have finite deadlines. Synchronous framework decoding cannot be forcibly interrupted by Go context alone, so the parent kills the isolated helper on cancellation/deadline. This is process isolation, not a macOS security sandbox.

## Verification boundary

Cloud Linux tests run the real external FFmpeg/ffprobe software route, cancellation/error tests, native-independent GIF/compositor/timing tests, and cross-compilation. Building the Darwin/Windows binaries is not a runtime test of AVFoundation, VideoToolbox, Media Foundation, windowing, power use, decoding accuracy or throughput. Those platform paths still need real-device execution, including 24/25/29.97/60 fps input, portrait/rotation/anamorphic media, disposal-heavy transparent GIFs, last-frame timing, cancellation, busy codecs and memory-pressure failures.

## Primary references

- [Apple AVAssetWriter](https://developer.apple.com/documentation/avfoundation/avassetwriter)
- [Apple AVAssetImageGenerator](https://developer.apple.com/documentation/avfoundation/avassetimagegenerator)
- [Apple pixel-buffer adaptor](https://developer.apple.com/documentation/avfoundation/avassetwriterinputpixelbufferadaptor)
- [Apple CVPixelBufferPoolCreatePixelBuffer](https://developer.apple.com/documentation/corevideo/cvpixelbufferpoolcreatepixelbuffer(_:_:_:))
- [FFmpeg Media Foundation implementation](https://raw.githubusercontent.com/FFmpeg/FFmpeg/master/libavcodec/mfenc.c), especially `mf_encv_output_adjust` and its GOP/CFR settings
- [FFmpeg VideoToolbox implementation](https://raw.githubusercontent.com/FFmpeg/FFmpeg/master/libavcodec/videotoolboxenc.c)
- [FFmpeg GIF demuxer options](https://ffmpeg.org/ffmpeg-formats.html#gif), including `ignore_loop`
- [FFmpeg scaling/frame-rate filters](https://ffmpeg.org/ffmpeg-filters.html)
