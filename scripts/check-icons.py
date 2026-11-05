#!/usr/bin/env python3
"""Validate checked-in icon assets without third-party modules.

Developer-only regeneration uses Pillow (scripts/build-icons.py). Release
checks stay dependency-free: this script only parses PNG/ICO/ICNS/COFF headers.
"""
from __future__ import annotations

import json
import struct
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
ICON_DIR = ROOT / "assets" / "icon"
WINDOWS_SIZES = [16, 20, 24, 32, 40, 48, 64, 128, 256]
ICNS_ELEMENTS = [(b"icp4", 16), (b"icp5", 32), (b"icp6", 64), (b"ic07", 128),
                 (b"ic08", 256), (b"ic09", 512), (b"ic10", 1024), (b"ic11", 32),
                 (b"ic12", 64), (b"ic13", 256), (b"ic14", 512)]
PNG_SIG = b"\x89PNG\r\n\x1a\n"
DIB_HEADER = b"\x28\x00\x00\x00"


def fail(msg: str) -> None:
    raise SystemExit("icon check failed: " + msg)


def png_info(path: Path) -> tuple[int, int, int, int]:
    data = path.read_bytes()
    if not data.startswith(PNG_SIG) or len(data) < 33:
        fail(f"{path}: not a PNG")
    length, kind = struct.unpack(">I4s", data[8:16])
    if kind != b"IHDR" or length != 13:
        fail(f"{path}: missing IHDR")
    width, height, depth, color = struct.unpack(">IIBB", data[16:26])
    return width, height, depth, color


def check_png(path: Path, size: int) -> None:
    if not path.is_file():
        fail(f"missing {path.relative_to(ROOT)}")
    width, height, depth, color = png_info(path)
    if (width, height) != (size, size) or depth != 8 or color != 6:
        fail(f"{path.relative_to(ROOT)}: {width}x{height} depth={depth} color={color}, "
             f"want {size}x{size} depth=8 RGBA")


def check_ico(path: Path) -> None:
    data = path.read_bytes()
    if len(data) < 6:
        fail(f"{path}: truncated ICO")
    reserved, kind, count = struct.unpack("<HHH", data[:6])
    if reserved != 0 or kind != 1 or count != len(WINDOWS_SIZES):
        fail(f"{path}: ICONDIR reserved={reserved} type={kind} count={count}")
    found = []
    for i in range(count):
        off = 6 + i * 16
        if off + 16 > len(data):
            fail(f"{path}: truncated ICONDIRENTRY {i}")
        w, h, _, _, planes, bits, size, offset = struct.unpack("<BBBBHHII", data[off:off + 16])
        w = w or 256
        h = h or 256
        if w != h or planes != 1 or bits != 32:
            fail(f"{path}: frame {i} is {w}x{h} planes={planes} bits={bits}")
        if offset + size > len(data) or size == 0:
            fail(f"{path}: frame {i} exceeds file bounds")
        payload = data[offset:offset + size]
        # Small frames stay DIB so NSIS and pre-PNG Windows loaders can read
        # them; 128 px and up use PNG to keep the file small.
        if w >= 128 and not payload.startswith(PNG_SIG):
            fail(f"{path}: frame {i} ({w}px) is not PNG")
        if w < 128 and payload[:4] != DIB_HEADER:
            fail(f"{path}: frame {i} ({w}px) is not a 32-bit DIB")
        found.append(w)
    if sorted(found) != WINDOWS_SIZES:
        fail(f"{path}: frames {sorted(found)}, want {WINDOWS_SIZES}")


def check_icns(path: Path) -> None:
    data = path.read_bytes()
    if len(data) < 8 or data[:4] != b"icns":
        fail(f"{path}: bad ICNS magic")
    total = struct.unpack(">I", data[4:8])[0]
    if total != len(data):
        fail(f"{path}: header length {total} != file length {len(data)}")
    pos, elements = 8, []
    for kind, size in ICNS_ELEMENTS:
        if pos + 8 > total:
            fail(f"{path}: missing element {kind.decode()}")
        got_kind = data[pos:pos + 4]
        length = struct.unpack(">I", data[pos + 4:pos + 8])[0]
        if got_kind != kind or length < 8 or pos + length > total:
            fail(f"{path}: element {got_kind!r} length {length} at {pos}, want {kind.decode()}")
        if not data[pos + 8:pos + 16].startswith(PNG_SIG):
            fail(f"{path}: element {kind.decode()} is not PNG-backed")
        width, height, depth, color = struct.unpack(">IIBB", data[pos + 24:pos + 34])
        if (width, height, depth, color) != (size, size, 8, 6):
            fail(f"{path}: element {kind.decode()} is {width}x{height} "
                 f"depth={depth} color={color}, want {size}x{size} depth=8 RGBA")
        elements.append(kind)
        pos += length
    if pos != total:
        fail(f"{path}: {total - pos} trailing bytes after ICNS elements")
    if elements != [kind for kind, _ in ICNS_ELEMENTS]:
        fail(f"{path}: elements {[k.decode() for k in elements]}")


def check_syso(path: Path, machine: int, reloc_type: int) -> None:
    """Check the COFF wrapper and the embedded icon group, not just the machine.

    go-winres re-encodes every frame with Go's PNG encoder, so the payload
    cannot be compared byte-for-byte with assets/icon/windows/*.png. Parsing
    the group icon and the PNG sizes still catches a stale syso whose frame
    list no longer matches winres.json.
    """
    if not path.is_file() or path.stat().st_size < 1000:
        fail(f"missing or empty {path.relative_to(ROOT)}")
    data = path.read_bytes()
    got, sections = struct.unpack_from("<HH", data, 0)
    if got != machine:
        fail(f"{path.relative_to(ROOT)}: machine {got:#06x}, want {machine:#06x}")
    if sections != 1 or struct.unpack_from("<H", data, 16)[0] != 0:
        fail(f"{path.relative_to(ROOT)}: expected one .rsrc section without an optional header")
    if data[20:28].rstrip(b"\0") != b".rsrc":
        fail(f"{path.relative_to(ROOT)}: first section is not .rsrc")
    raw_size, raw_ptr = struct.unpack_from("<II", data, 20 + 16)
    reloc_ptr = struct.unpack_from("<I", data, 20 + 24)[0]
    reloc_count = struct.unpack_from("<H", data, 20 + 32)[0]
    if raw_ptr + raw_size > len(data):
        fail(f"{path.relative_to(ROOT)}: .rsrc exceeds file bounds")
    if reloc_count != 10:
        fail(f"{path.relative_to(ROOT)}: {reloc_count} relocations, want 10")
    for i in range(reloc_count):
        typ = struct.unpack_from("<H", data, reloc_ptr + i * 10 + 8)[0]
        if typ != reloc_type:
            fail(f"{path.relative_to(ROOT)}: relocation {i} type {typ}, want {reloc_type}")

    rsrc = data[raw_ptr:raw_ptr + raw_size]
    marker = b"\x00\x00\x01\x00" + struct.pack("<H", len(WINDOWS_SIZES))
    if rsrc.count(marker) != 1:
        fail(f"{path.relative_to(ROOT)}: no unique RT_GROUP_ICON header")
    pos = rsrc.find(marker) + 6
    entries = []
    for i in range(len(WINDOWS_SIZES)):
        width, height, _colors, _reserved, planes, bits, _bytes, ident = struct.unpack_from(
            "<BBBBHHIH", rsrc, pos + i * 14)
        entries.append((width or 256, height or 256, planes, bits, ident))
    expected = [(size, size, 1, 32, i + 1)
                for i, size in enumerate(reversed(WINDOWS_SIZES))]
    if entries != expected:
        fail(f"{path.relative_to(ROOT)}: group frames {entries}, want {expected}")

    dims, pos = [], 0
    while True:
        pos = rsrc.find(PNG_SIG, pos)
        if pos < 0:
            break
        if rsrc[pos + 12:pos + 16] == b"IHDR":
            dims.append(struct.unpack_from(">II", rsrc, pos + 16))
        pos += len(PNG_SIG)
    if sorted(dims) != [(size, size) for size in WINDOWS_SIZES]:
        fail(f"{path.relative_to(ROOT)}: embedded PNG sizes {sorted(dims)}")


def main() -> int:
    check_png(ICON_DIR / "wallpaper-source.png", 1024)
    for size in WINDOWS_SIZES:
        check_png(ICON_DIR / "windows" / f"icon-{size}.png", size)
    check_ico(ICON_DIR / "wallpaper.ico")
    check_icns(ICON_DIR / "AppIcon.icns")
    syso_dir = ROOT / "cmd" / "wallpaperd"
    if {p.name for p in syso_dir.glob("*.syso")} != {
            "rsrc_windows_amd64.syso", "rsrc_windows_arm64.syso"}:
        fail(f"unexpected .syso set in {syso_dir.relative_to(ROOT)}")
    check_syso(syso_dir / "rsrc_windows_amd64.syso", 0x8664, 3)
    check_syso(syso_dir / "rsrc_windows_arm64.syso", 0xAA64, 2)

    winres = json.loads((ICON_DIR / "winres.json").read_text())
    frames = winres["RT_GROUP_ICON"]["APP"]["0000"]
    if len(frames) != len(WINDOWS_SIZES):
        fail(f"winres.json has {len(frames)} icon frames, want {len(WINDOWS_SIZES)}")
    for frame in frames:
        path = ICON_DIR / frame
        expected = int(path.stem.removeprefix("icon-"))
        check_png(path, expected)

    print(f"PASS: source + {len(WINDOWS_SIZES)} Windows frames + ICO {WINDOWS_SIZES}; "
          f"ICNS {[k.decode() for k, _ in ICNS_ELEMENTS]}; amd64/arm64 .syso "
          f"with {len(WINDOWS_SIZES)} icon frames.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
