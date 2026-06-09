#!/usr/bin/env python3
"""Generate the 武陵 DevOps logo system (方案 A · 青蓝切角印).

Single source of truth for the brand marks. Re-run after tweaking the palette
or geometry below.

  SVGs  -> need only python3
  PNGs  -> additionally need `rsvg-convert` and ImageMagick (`magick`)

The 「武」 glyph outline is embedded (WU_PATH) — extracted once from
Noto Sans SC Bold (SIL OFL 1.1), so regenerating needs no font or network.

Outputs (paths relative to this file):
  ../frontend/public/favicon.svg          adaptive chamfered mark (served)
  ../frontend/public/apple-touch-icon.png 180, opaque
  ../frontend/public/icon-192.png         PWA, opaque
  ../frontend/public/icon-512.png         PWA, opaque
  ./logo-mark.svg                          fixed-teal chamfered mark (docs)
  ./icon-fullbleed.svg                     app-icon master (full bleed)
  ./icon-maskable.svg                      tighter safe-zone reference
  ./icon-1024.png                          iOS master, opaque
"""
import math, shutil, subprocess, sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
PUB = HERE.parent / "frontend" / "public"

# 「武」 (U+6B66), Noto Sans SC Bold. Font space: UPM 1000, y-up,
# ink bounds (31,-91)-(970,845); ink centre (500.5, 377).
WU_PATH = "M720 776 803 837Q829 818 855.5 793.5Q882 769 905.0 745.0Q928 721 941 700L853 632Q841 653 819.0 678.0Q797 703 771.5 729.0Q746 755 720 776ZM50 617H950V507H50ZM359 366H559V261H359ZM127 804H507V698H127ZM31 40Q101 50 192.0 65.0Q283 80 384.0 97.5Q485 115 583 133L592 21Q499 3 404.5 -14.5Q310 -32 221.5 -48.0Q133 -64 61 -77ZM299 479H414V24H299ZM114 414H224V11H114ZM573 845H695Q692 719 697.5 599.5Q703 480 716.0 376.5Q729 273 747.5 194.5Q766 116 788.5 72.0Q811 28 837 28Q852 28 860.0 71.5Q868 115 871 210Q891 190 919.0 171.0Q947 152 970 143Q963 50 946.0 -1.0Q929 -52 900.0 -71.5Q871 -91 826 -91Q775 -91 737.0 -54.0Q699 -17 671.0 48.5Q643 114 624.5 202.5Q606 291 595.0 395.5Q584 500 578.5 614.5Q573 729 573 845Z"
GCX, GCY = 500.5, 377.0

# --- palette (hue ~201, 青蓝) ----------------------------------------------
TEAL_LIGHT = "#6ea7c9"
TEAL_BRAND = "#5a8fb0"   # == clean theme --accent / manifest theme_color
TEAL_DEEP  = "#3d6e8a"
CUT_HI     = "#a9dcef"   # terminal "cut" highlight


def glyph_group(cx, cy, side, fill="#ffffff"):
    """Place 武 centred at (cx,cy) fitting `side` units; flip y-up -> y-down."""
    s = side / 939.0
    tx = cx - s * GCX
    ty = cy + s * GCY
    return (f'<g transform="translate({tx:.3f},{ty:.3f}) scale({s:.5f},{-s:.5f})">'
            f'<path d="{WU_PATH}" fill="{fill}"/></g>')


def grad(id_, c0, c1):
    return (f'<linearGradient id="{id_}" x1="0" y1="0" x2="0" y2="100%">'
            f'<stop offset="0" stop-color="{c0}"/>'
            f'<stop offset="1" stop-color="{c1}"/></linearGradient>')


def tile_path(r=16, cut=34):
    """Rounded square with the top-right corner chamfered (the Endfield tell)."""
    return (f"M {r},0 H {100-cut} L 100,{cut} V {100-r} "
            f"A {r} {r} 0 0 1 {100-r},100 H {r} "
            f"A {r} {r} 0 0 1 0,{100-r} V {r} "
            f"A {r} {r} 0 0 1 {r},0 Z")


def chamfer_accent():
    nx, ny = -1 / math.sqrt(2), 1 / math.sqrt(2)   # inward (down-left)
    d = 7.5
    p0 = (66 + nx * d, 0 + ny * d)
    p1 = (100 + nx * d, 34 + ny * d)
    lerp = lambda a, b, t: (a[0] + (b[0]-a[0])*t, a[1] + (b[1]-a[1])*t)
    a, b = lerp(p0, p1, 0.12), lerp(p0, p1, 0.88)
    return (f'<line x1="{a[0]:.2f}" y1="{a[1]:.2f}" x2="{b[0]:.2f}" y2="{b[1]:.2f}" '
            f'stroke="{CUT_HI}" stroke-width="2.6" stroke-linecap="round" opacity=".9"/>')


def chamfered_mark(adaptive=True):
    css = ('<style>@media (prefers-color-scheme:dark){'
           '.ring{stroke:#7fb4d0;stroke-opacity:.55}}</style>') if adaptive else ""
    return f'''<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100" width="100" height="100" role="img" aria-label="武陵 DevOps">
{css}<defs>{grad("bg", TEAL_LIGHT, TEAL_DEEP)}
<linearGradient id="sheen" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#fff" stop-opacity=".18"/><stop offset=".5" stop-color="#fff" stop-opacity="0"/></linearGradient></defs>
<path d="{tile_path()}" fill="url(#bg)"/>
<path d="{tile_path()}" fill="url(#sheen)"/>
{glyph_group(50, 52, 60)}
{chamfer_accent()}
<path class="ring" d="{tile_path()}" fill="none" stroke="#1f3d52" stroke-opacity=".25" stroke-width="1.5"/>
</svg>'''


def fullbleed(side=1024, mark_frac=0.60):
    S = side
    step = S / 12
    grid = "".join(f'<line x1="{i*step:.1f}" y1="0" x2="{i*step:.1f}" y2="{S}"/>'
                   f'<line x1="0" y1="{i*step:.1f}" x2="{S}" y2="{i*step:.1f}"/>'
                   for i in range(1, 12))
    cutlen = S * 0.20
    return f'''<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {S} {S}" width="{S}" height="{S}">
<defs>{grad("bgf", TEAL_LIGHT, TEAL_DEEP)}
<linearGradient id="sheenf" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#fff" stop-opacity=".16"/><stop offset=".55" stop-color="#fff" stop-opacity="0"/></linearGradient></defs>
<rect width="{S}" height="{S}" fill="url(#bgf)"/>
<g stroke="#fff" stroke-opacity=".05" stroke-width="1">{grid}</g>
<rect width="{S}" height="{S}" fill="url(#sheenf)"/>
<g stroke="{CUT_HI}" stroke-width="{S*0.014:.1f}" stroke-linecap="round" opacity=".85"><line x1="{S-cutlen}" y1="{S*0.07}" x2="{S-S*0.07}" y2="{cutlen}"/></g>
{glyph_group(S/2, S*0.515, mark_frac*S)}
</svg>'''


def main():
    (PUB / "favicon.svg").write_text(chamfered_mark(adaptive=True))
    (HERE / "logo-mark.svg").write_text(chamfered_mark(adaptive=False))
    (HERE / "icon-fullbleed.svg").write_text(fullbleed(1024, 0.60))
    (HERE / "icon-maskable.svg").write_text(fullbleed(512, 0.52))
    print("✓ SVGs written")

    rsvg, magick = shutil.which("rsvg-convert"), shutil.which("magick")
    if not (rsvg and magick):
        print("⚠ rsvg-convert / magick not found — skipping PNGs", file=sys.stderr)
        return
    src = str(HERE / "icon-fullbleed.svg")
    tmp = HERE / "_tmp.png"
    for size, out in [(192, PUB/"icon-192.png"), (512, PUB/"icon-512.png"),
                      (180, PUB/"apple-touch-icon.png"), (1024, HERE/"icon-1024.png")]:
        subprocess.run([rsvg, "-w", str(size), "-h", str(size), src, "-o", str(tmp)], check=True)
        # strip alpha — iOS app icons must be opaque
        subprocess.run([magick, str(tmp), "-alpha", "remove", "-alpha", "off", "-strip", str(out)], check=True)
    tmp.unlink(missing_ok=True)
    print("✓ PNGs written")


if __name__ == "__main__":
    main()
