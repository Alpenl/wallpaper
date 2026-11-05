#!/usr/bin/env python3
"""Build Windows/macOS app-icon assets from the transparent 1024 px master.

The master (assets/icon/wallpaper-source.png) is a rounded-square RGBA image.
Windows keeps a small transparent margin (92% content) so the icon does not
look oversized in the taskbar; macOS uses the Big Sur inset (824/1024 = 80.5%).
Frames up to 32 px come from a deterministic simplified master so the mountain
and play mark stay separated at taskbar sizes. Requires Pillow (only developers
regenerating icons need it; release builds consume the checked-in outputs).
"""
from __future__ import annotations

import argparse
import struct
import sys
from pathlib import Path

from PIL import Image, ImageDraw, ImageFilter

WINDOWS_SIZES = [16, 20, 24, 32, 40, 48, 64, 128, 256]
WINDOWS_CONTENT = 0.92
MACOS_CONTENT = 824 / 1024

# ICNS element types and the pixel size each one carries.
ICNS_TYPES = [
    (b"icp4", 16),
    (b"icp5", 32),
    (b"icp6", 64),
    (b"ic07", 128),
    (b"ic08", 256),
    (b"ic09", 512),
    (b"ic10", 1024),
    (b"ic11", 32),
    (b"ic12", 64),
    (b"ic13", 256),
    (b"ic14", 512),
]


def load_master(path: Path) -> Image.Image:
    im = Image.open(path)
    if im.mode != "RGBA":
        raise SystemExit(f"{path}: expected RGBA, got {im.mode}")
    if im.size != (1024, 1024):
        raise SystemExit(f"{path}: expected 1024x1024, got {im.size}")
    return im


def inset(im: Image.Image, size: int, content: float) -> Image.Image:
    """Scale the icon into the center of a transparent square canvas."""
    canvas = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    inner = max(1, round(size * content))
    scaled = im.resize((inner, inner), Image.Resampling.LANCZOS)
    offset = (size - inner) // 2
    canvas.alpha_composite(scaled, (offset, offset))
    return canvas


def small_master() -> Image.Image:
    """Draw a deliberately simplified master for 16-32 px frames.

    The generated 1024 px artwork has a small mountain-to-play gutter and three
    overlapping sheets; both collapse below ~32 px. This vector-style variant
    keeps the same story with one broad mountain peak and one bold play mark,
    and is only used for the small ICO/ICNS entries. Two peaks were tried and
    turned into two separate blobs at 16 px; a single flat-based peak with a
    wide gutter stays readable down to 16 px.
    """
    size = 1024
    canvas = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    gradient = Image.new("RGBA", (size, size))
    gd = ImageDraw.Draw(gradient)
    top, bottom = (0, 8, 68), (0, 34, 130)
    for y in range(size):
        t = y / (size - 1)
        gd.line((0, y, size, y), fill=tuple(
            round(top[i] + (bottom[i] - top[i]) * t) for i in range(3)) + (255,))
    # The generated master has a bright blue glow along the bottom edge; a soft
    # ellipse keeps the small frames from looking like a different, darker icon
    # next to the 48 px and larger generated frames.
    glow = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    ImageDraw.Draw(glow).ellipse((40, 560, 984, 1180), fill=(0, 80, 250, 255))
    gradient = Image.alpha_composite(gradient, glow.filter(ImageFilter.GaussianBlur(120)))
    mask = Image.new("L", (size, size), 0)
    ImageDraw.Draw(mask).rounded_rectangle((0, 0, size - 1, size - 1), radius=225, fill=255)
    canvas.paste(gradient, (0, 0), mask)
    d = ImageDraw.Draw(canvas)
    d.rounded_rectangle((200, 105, 790, 815), radius=60, fill=(0, 55, 200, 255))
    d.rounded_rectangle((230, 135, 810, 840), radius=65, fill=(95, 188, 254, 255))
    d.rounded_rectangle((220, 175, 805, 885), radius=70, fill=(247, 247, 242, 255))
    ink = (1, 29, 150, 255)
    d.polygon([(285, 545), (510, 270), (740, 545)], fill=ink)
    d.rectangle((285, 515, 740, 545), fill=ink)
    d.polygon([(390, 590), (390, 820), (610, 705)], fill=ink)
    return canvas


def png_bytes(im: Image.Image) -> bytes:
    import io

    buf = io.BytesIO()
    im.save(buf, format="PNG", optimize=True)
    return buf.getvalue()


def dib_bytes(im: Image.Image) -> bytes:
    """Encode a 32-bit bottom-up DIB icon frame (with an empty AND mask).

    Small frames stay in DIB form so NSIS and older Windows icon loaders that
    predate PNG-compressed ICO entries can still read the installer icon.
    """
    im = im.convert("RGBA")
    w, h = im.size
    rows = []
    for y in range(h - 1, -1, -1):
        row = bytearray()
        for x in range(w):
            r, g, b, a = im.getpixel((x, y))
            row += bytes((b, g, r, a))
        rows.append(bytes(row))
    xor = b"".join(rows)
    mask = b"\x00" * (((w + 31) // 32) * 4) * h
    header = struct.pack("<IiiHHIIiiII", 40, w, h * 2, 1, 32, 0,
                         len(xor) + len(mask), 0, 0, 0, 0)
    return header + xor + mask


def windows_frame(master: Image.Image, small: Image.Image, size: int) -> Image.Image:
    if size <= 32:
        return inset(small, size, WINDOWS_CONTENT)
    return inset(master, 1024, WINDOWS_CONTENT).resize((size, size), Image.Resampling.LANCZOS)


def build_ico(master: Image.Image, out: Path) -> None:
    small = small_master()
    entries, payloads, offset = [], [], 6 + 16 * len(WINDOWS_SIZES)
    for size in WINDOWS_SIZES:
        im = windows_frame(master, small, size)
        data = png_bytes(im) if size >= 128 else dib_bytes(im)
        entries.append(struct.pack(
            "<BBBBHHII", size if size < 256 else 0, size if size < 256 else 0,
            0, 0, 1, 32, len(data), offset))
        payloads.append(data)
        offset += len(data)
    out.write_bytes(struct.pack("<HHH", 0, 1, len(WINDOWS_SIZES)) +
                    b"".join(entries) + b"".join(payloads))


def build_icns(master: Image.Image, out: Path) -> None:
    mac = inset(master, 1024, MACOS_CONTENT)
    mac_small = inset(small_master(), 1024, MACOS_CONTENT)
    chunks = []
    for kind, size in ICNS_TYPES:
        source = mac_small if size <= 64 else mac
        data = png_bytes(source.resize((size, size), Image.Resampling.LANCZOS))
        chunks.append(kind + struct.pack(">I", len(data) + 8) + data)
    body = b"".join(chunks)
    out.write_bytes(b"icns" + struct.pack(">I", len(body) + 8) + body)


def main() -> int:
    root = Path(__file__).resolve().parent.parent
    ap = argparse.ArgumentParser()
    ap.add_argument("--source", default=str(root / "assets/icon/wallpaper-source.png"))
    ap.add_argument("--out-dir", default=str(root / "assets/icon"))
    args = ap.parse_args()

    src = Path(args.source)
    out_dir = Path(args.out_dir)
    out_dir.mkdir(parents=True, exist_ok=True)
    master = load_master(src)

    # go-winres consumes individual PNG frames, not .ico containers.
    win_png = out_dir / "windows"
    win_png.mkdir(parents=True, exist_ok=True)
    small = small_master()
    for size in WINDOWS_SIZES:
        windows_frame(master, small, size).save(win_png / f"icon-{size}.png", optimize=True)

    ico = out_dir / "wallpaper.ico"
    icns = out_dir / "AppIcon.icns"
    build_ico(master, ico)
    build_icns(master, icns)

    # Verify the outputs are readable and carry the expected frame count.
    with Image.open(ico) as check:
        ico_sizes = sorted(check.info.get("sizes", set()))
    if len(ico_sizes) != len(WINDOWS_SIZES):
        raise SystemExit(f"{ico}: expected {len(WINDOWS_SIZES)} frames, got {ico_sizes}")
    data = icns.read_bytes()
    if data[:4] != b"icns" or struct.unpack(">I", data[4:8])[0] != len(data):
        raise SystemExit(f"{icns}: invalid ICNS header")
    print(f"source {src} ({master.size[0]}x{master.size[1]})")
    for path in [ico, icns, *(win_png / f"icon-{s}.png" for s in WINDOWS_SIZES)]:
        shown = path.relative_to(root) if path.is_relative_to(root) else path
        print(f"wrote {shown} ({path.stat().st_size} bytes)")
    print(f"ico frames: {ico_sizes}")
    print(f"icns elements: {[kind.decode() for kind, _ in ICNS_TYPES]}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
