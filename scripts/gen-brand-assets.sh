#!/usr/bin/env bash
# Regenerate every brand raster the frontend ships from the master logo.
#
# Source of truth: assets/wuling-logo-3.png (1024x1024, 16-bit, no alpha).
# The sibling .svg is NOT vector — it is the same raster base64-embedded in an
# <image> tag by Pixelmator — so there is nothing to gain by rasterizing it and
# a 67 KB "SVG" favicon to lose. Everything below derives from the PNG.
#
# Outputs land in frontend/public/, which the Nasti build copies into dist/ via
# the copy-public plugin in frontend/nasti.config.ts.
#
# Requires ImageMagick 7 (`brew install imagemagick`).
#
# Usage:  ./scripts/gen-brand-assets.sh
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
src="$repo_root/assets/wuling-logo-3.png"
out="$repo_root/frontend/public"

# The flat field the wordmark sits on. Sampled from the master at p{5,5}; also
# the manifest's background_color. Keyed out (with a fuzz tolerance, because the
# antialiased stroke edges blend into it) to produce the transparent wordmark.
FIELD="#D0E0E3"

command -v magick >/dev/null || {
  echo "magick (ImageMagick 7) not found — brew install imagemagick" >&2
  exit 1
}
[ -f "$src" ] || {
  echo "master logo missing: $src" >&2
  exit 1
}

mkdir -p "$out"

# --- square app icons -------------------------------------------------------
# Kept full-bleed (the field reaches every edge) and 8-bit: the master is 16-bit
# TrueColor, which doubles the byte size for depth no icon renderer can use.
for size in 512 192; do
  magick "$src" -depth 8 -strip -filter Lanczos -resize "${size}x${size}" \
    -define png:compression-level=9 "$out/icon-${size}.png"
done

# Maskable variant. Android masks to a shape and crops ~10% per side, so the
# safe zone is the central 80%. The wordmark already occupies only the central
# ~61% x 28%, so the master needs no extra padding — but the manifest still has
# to declare a `purpose: maskable` entry, and pointing that at its own file
# keeps the two decoupled if the master's framing ever changes.
magick "$src" -depth 8 -strip -filter Lanczos -resize 512x512 \
  -define png:compression-level=9 "$out/icon-maskable-512.png"

# iOS home screen. Never transparent (iOS composites onto black), so the
# full-bleed field is exactly right.
magick "$src" -depth 8 -strip -filter Lanczos -resize 180x180 \
  -define png:compression-level=9 "$out/apple-touch-icon.png"

# --- favicons ---------------------------------------------------------------
magick "$src" -depth 8 -strip -filter Lanczos -resize 32x32 \
  -define png:compression-level=9 "$out/favicon-32.png"

# Multi-resolution .ico for browsers (and Windows pinned sites) that still ask
# for one. 48/32/16 in a single file.
magick "$src" -depth 8 -strip -filter Lanczos \
  \( -clone 0 -resize 48x48 \) \
  \( -clone 0 -resize 32x32 \) \
  \( -clone 0 -resize 16x16 \) \
  -delete 0 "$out/favicon.ico"

# --- in-app wordmark --------------------------------------------------------
# Transparent, trimmed to the ink so the app shell can size it by height and
# let it sit on any theme surface. Emitted at 2x the largest on-screen size
# (the auth-page lockup renders it ~30px tall) for retina.
magick "$src" -depth 8 -strip \
  -fuzz 12% -transparent "$FIELD" -trim +repage \
  -resize x120 -define png:compression-level=9 "$out/brand-wordmark.png"

# --- social preview ---------------------------------------------------------
# 1200x630 og:image: the field as the canvas, wordmark centred at ~46% width.
magick -size 1200x630 "xc:$FIELD" \
  \( "$out/brand-wordmark.png" -resize 552x \) \
  -gravity center -composite -depth 8 -strip \
  -define png:compression-level=9 "$out/og-image.png"

echo "Regenerated brand assets in frontend/public:"
cd "$out" && ls -1 icon-*.png apple-touch-icon.png favicon-32.png favicon.ico \
  brand-wordmark.png og-image.png | sed 's/^/  /'
