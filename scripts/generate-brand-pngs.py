#!/usr/bin/env python3
"""Rasterize VaultRun brand + LinkedIn assets from the SVG mark geometry.

Requires: Pillow, JetBrains Mono (or DejaVu Sans Mono fallback).
Run from repo root:  python3 scripts/generate-brand-pngs.py
"""

from __future__ import annotations

from pathlib import Path

from PIL import Image, ImageDraw, ImageFilter, ImageFont

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / "site" / "brand"

BG = (10, 10, 10, 255)
FG = (232, 232, 232, 255)
DIM = (138, 138, 138, 255)
LINE = (38, 38, 38, 255)
FAINT = (85, 85, 85, 255)
WHITE = (255, 255, 255, 255)
BLACK = (10, 10, 10, 255)

FONT_CANDIDATES = [
    "/usr/share/fonts/truetype/jetbrains-mono/JetBrainsMono-Bold.ttf",
    "/usr/share/fonts/truetype/jetbrains-mono/JetBrainsMono-SemiBold.ttf",
    "/usr/share/fonts/truetype/jetbrains-mono/JetBrainsMono-Regular.ttf",
    "/usr/share/fonts/truetype/dejavu/DejaVuSansMono-Bold.ttf",
    "/usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf",
]
FONT_REG_CANDIDATES = [
    "/usr/share/fonts/truetype/jetbrains-mono/JetBrainsMono-Regular.ttf",
    "/usr/share/fonts/truetype/jetbrains-mono/JetBrainsMono-Medium.ttf",
    "/usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf",
]


def font_path(candidates: list[str]) -> str:
    for p in candidates:
        if Path(p).is_file():
            return p
    raise SystemExit("No monospace font found (install JetBrains Mono or DejaVu Sans Mono)")


BOLD = font_path(FONT_CANDIDATES)
REG = font_path(FONT_REG_CANDIDATES)


def load_font(path: str, size: int) -> ImageFont.FreeTypeFont:
    return ImageFont.truetype(path, size)


def draw_spaced(draw: ImageDraw.ImageDraw, xy: tuple[float, float], text: str, font: ImageFont.FreeTypeFont, fill, tracking: float = 0.0) -> float:
    """Draw text with extra letter-spacing; returns final x."""
    x, y = xy
    for i, ch in enumerate(text):
        draw.text((x, y), ch, font=font, fill=fill)
        x += draw.textlength(ch, font=font) + tracking
    return x


def text_width(draw: ImageDraw.ImageDraw, text: str, font: ImageFont.FreeTypeFont, tracking: float = 0.0) -> float:
    if not text:
        return 0.0
    w = sum(draw.textlength(ch, font=font) for ch in text)
    return w + tracking * max(0, len(text) - 1)


def draw_mark(draw: ImageDraw.ImageDraw, x: float, y: float, size: float, fg, *, stroke_scale: float | None = None) -> None:
    """Terminal prompt >_ inside a square frame (SVG viewBox 64)."""
    s = size / 64.0
    sw = max(1.0, (2.5 if stroke_scale is None else stroke_scale) * s)
    # outer frame
    inset = 8 * s
    draw.rectangle(
        [x + inset, y + inset, x + size - inset, y + size - inset],
        outline=fg,
        width=max(1, round(sw)),
    )
    # chevron >
    p1 = (x + 20 * s, y + 22 * s)
    p2 = (x + 34 * s, y + 32 * s)
    p3 = (x + 20 * s, y + 42 * s)
    draw.line([p1, p2, p3], fill=fg, width=max(1, round(3.2 * s)), joint="miter")
    # cursor block _
    bx0 = x + 38 * s
    by0 = y + 28 * s
    bx1 = x + 48 * s
    by1 = y + 36 * s
    draw.rectangle([bx0, by0, bx1, by1], fill=fg)


def save(img: Image.Image, name: str) -> None:
    path = OUT / name
    rgb = img.convert("RGB") if img.mode == "RGBA" else img
    rgb.save(path, "PNG", optimize=True)
    kb = path.stat().st_size / 1024
    print(f"  {name:32s} {rgb.size[0]:4d}×{rgb.size[1]:<4d}  {kb:7.1f} KB")


def save_jpeg(img: Image.Image, name: str, quality: int = 93) -> None:
    path = OUT / name
    rgb = img.convert("RGB")
    rgb.save(path, "JPEG", quality=quality, optimize=True, subsampling=0)
    kb = path.stat().st_size / 1024
    print(f"  {name:32s} {rgb.size[0]:4d}×{rgb.size[1]:<4d}  {kb:7.1f} KB")


def glow_layer(size: tuple[int, int], blobs: list[tuple[float, float, float, int]]) -> Image.Image:
    """Soft white glows: (cx, cy, radius, peak_alpha)."""
    layer = Image.new("L", size, 0)
    for cx, cy, radius, peak in blobs:
        blob = Image.new("L", size, 0)
        d = ImageDraw.Draw(blob)
        d.ellipse([cx - radius, cy - radius, cx + radius, cy + radius], fill=peak)
        blob = blob.filter(ImageFilter.GaussianBlur(radius=max(8, radius * 0.45)))
        layer = Image.composite(blob, layer, blob)
    return layer


def apply_glow(base: Image.Image, blobs: list[tuple[float, float, float, int]]) -> Image.Image:
    img = base.convert("RGBA")
    mask = glow_layer(img.size, blobs)
    white = Image.new("RGBA", img.size, (232, 232, 232, 255))
    glow = Image.new("RGBA", img.size, (0, 0, 0, 0))
    glow.paste(white, (0, 0), mask)
    return Image.alpha_composite(img, glow)


def draw_grid(draw: ImageDraw.ImageDraw, w: int, h: int, step: int, color=LINE) -> None:
    for x in range(0, w, step):
        draw.line([(x, 0), (x, h)], fill=color)
    for y in range(0, h, step):
        draw.line([(0, y), (w, y)], fill=color)


def mark_square(size: int, *, light: bool = False) -> Image.Image:
    bg, fg = (WHITE, BLACK) if light else (BG, FG)
    img = Image.new("RGBA", (size, size), bg)
    draw = ImageDraw.Draw(img)
    draw_mark(draw, 0, 0, size, fg)
    return img


def lockup(width: int, height: int, *, light: bool = False) -> Image.Image:
    bg, fg = (WHITE, BLACK) if light else (BG, FG)
    img = Image.new("RGBA", (width, height), bg)
    draw = ImageDraw.Draw(img)
    mark = int(height)
    draw_mark(draw, 0, 0, mark, fg)
    f = load_font(BOLD, max(18, int(height * 0.44)))
    tracking = max(1.0, height * 0.04)
    tx = mark + int(height * 0.12)
    ty = (height - f.size) / 2 - height * 0.06
    draw_spaced(draw, (tx, ty), "VAULTRUN", f, fg, tracking=tracking)
    return img


def linkedin_logo() -> Image.Image:
    """400×400 page logo. Self-contained dark mark so it reads on light and dark UI."""
    return mark_square(400, light=False)


def linkedin_cover() -> Image.Image:
    """4200×700 company cover (6:1). Type sits in the RIGHT half: LinkedIn overlays
    the page logo on the bottom-left of the live Page (and in the editor sidebar).
    Two lines only — a third line and a right-side watermark collapse at ~191px."""
    w, h = 4200, 700
    img = Image.new("RGBA", (w, h), BG)
    img = apply_glow(img, [(w * 0.62, -40, 820, 18)])
    draw = ImageDraw.Draw(img)
    draw_grid(draw, w, h, 70, (22, 22, 22, 255))
    draw.line([(0, 2), (w, 2)], fill=LINE, width=3)
    draw.line([(0, h - 3), (w, h - 3)], fill=LINE, width=3)

    word = load_font(BOLD, 200)
    tag = load_font(REG, 64)
    tracking = 16
    tag_line = "Self-hosted secure runtime for AI agents"
    word_w = text_width(draw, "VAULTRUN", word, tracking)
    tag_w = draw.textlength(tag_line, font=tag)
    block_w = max(word_w, tag_w)
    # Right-weighted, but not flush: 560px right margin (~64px on the 1128-wide
    # editor preview) so it sits off the edge without sliding back over the logo.
    tx = w - 560 - int(block_w)
    block_h = 200 + 24 + 64
    ty = (h - block_h) // 2
    draw_spaced(draw, (tx, ty), "VAULTRUN", word, FG, tracking=tracking)
    # Tagline shares the same left edge as the wordmark (left-aligned block on the right).
    draw.text((tx, ty + 200 + 24), tag_line, font=tag, fill=DIM)
    return img


def linkedin_profile_banner() -> Image.Image:
    """1584×396 personal-profile banner (founder)."""
    w, h = 1584, 396
    img = Image.new("RGBA", (w, h), BG)
    img = apply_glow(img, [(180, -20, 280, 26), (1400, 200, 260, 14)])
    draw = ImageDraw.Draw(img)
    draw_grid(draw, w, h, 44, (22, 22, 22, 255))
    mark_size = 168
    mx, my = 72, (h - mark_size) // 2
    draw_mark(draw, mx, my, mark_size, FG)
    title = load_font(BOLD, 54)
    sub = load_font(REG, 26)
    tx = mx + mark_size + 40
    draw_spaced(draw, (tx, 128), "VAULTRUN", title, FG, tracking=6)
    draw.text((tx, 200), "Building a self-hosted secure runtime for AI agents", font=sub, fill=DIM)
    url_f = load_font(REG, 22)
    url = "vaultrun.dev"
    uw = draw.textlength(url, font=url_f)
    draw.text((w - 64 - uw, h - 56), url, font=url_f, fill=FAINT)
    return img


def og_image() -> Image.Image:
    w, h = 1200, 630
    img = Image.new("RGBA", (w, h), BG)
    img = apply_glow(img, [(140, -30, 360, 28), (1080, 420, 300, 14)])
    draw = ImageDraw.Draw(img)
    draw_grid(draw, w, h, 48, (22, 22, 22, 255))
    draw.rectangle([0, 0, w - 1, h - 1], outline=LINE, width=2)
    mark_size = 96
    draw_mark(draw, 80, 72, mark_size, FG)
    word = load_font(BOLD, 36)
    draw_spaced(draw, (80 + mark_size + 24, 98), "VAULTRUN", word, FG, tracking=4)
    title = load_font(BOLD, 44)
    draw.text((80, 230), "Self-hosted secure runtime", font=title, fill=FG)
    draw.text((80, 286), "for AI agents.", font=title, fill=FG)
    sub = load_font(REG, 24)
    draw.text((80, 370), "Isolated Docker sandboxes  ·  MCP  ·  HMAC-signed audit", font=sub, fill=DIM)
    draw.text((80, 410), "Your infrastructure. No SaaS telemetry.", font=sub, fill=DIM)
    url_f = load_font(REG, 22)
    draw.text((80, h - 72), "vaultrun.dev", font=url_f, fill=FAINT)
    return img


def social_square() -> Image.Image:
    w = h = 1080
    img = Image.new("RGBA", (w, h), BG)
    img = apply_glow(img, [(180, 80, 320, 22), (900, 900, 280, 12)])
    draw = ImageDraw.Draw(img)
    draw_grid(draw, w, h, 54, (22, 22, 22, 255))
    draw.rectangle([48, 48, w - 49, h - 49], outline=LINE, width=2)
    mark_size = 160
    draw_mark(draw, (w - mark_size) // 2, 160, mark_size, FG)
    word = load_font(BOLD, 56)
    tw = text_width(draw, "VAULTRUN", word, tracking=8)
    draw_spaced(draw, ((w - tw) / 2, 360), "VAULTRUN", word, FG, tracking=8)
    sub = load_font(REG, 26)
    lines = [
        "self-hosted secure runtime",
        "for AI agents",
        "",
        "Docker sandboxes  ·  MCP  ·  audit",
        "your infrastructure",
    ]
    y = 460
    for line in lines:
        lw = draw.textlength(line, font=sub) if line else 0
        draw.text(((w - lw) / 2, y), line, font=sub, fill=DIM if line else DIM)
        y += 36 if line else 18
    url_f = load_font(REG, 22)
    url = "vaultrun.dev"
    uw = draw.textlength(url, font=url_f)
    draw.text(((w - uw) / 2, 940), url, font=url_f, fill=FAINT)
    return img


def first_post() -> Image.Image:
    """1080×1080 launch graphic — attach to the company-page first post."""
    w = h = 1080
    img = Image.new("RGBA", (w, h), BG)
    img = apply_glow(img, [(200, 40, 360, 24), (940, 980, 300, 12)])
    draw = ImageDraw.Draw(img)
    draw_grid(draw, w, h, 54, (22, 22, 22, 255))
    draw.rectangle([40, 40, w - 41, h - 41], outline=LINE, width=2)

    kicker = load_font(REG, 22)
    draw_spaced(draw, (88, 88), "NOW OPEN", kicker, FAINT, tracking=6)

    mark_size = 120
    draw_mark(draw, 80, 160, mark_size, FG)
    word = load_font(BOLD, 48)
    draw_spaced(draw, (80 + mark_size + 28, 196), "VAULTRUN", word, FG, tracking=6)

    title = load_font(BOLD, 40)
    draw.text((88, 360), "The sandbox agents need,", font=title, fill=FG)
    draw.text((88, 416), "on infrastructure", font=title, fill=FG)
    draw.text((88, 472), "security already trusts.", font=title, fill=FG)

    sub = load_font(REG, 26)
    bullets = [
        "Isolated Docker session per agent",
        "53+ MCP tools  ·  stdio + HTTP",
        "HMAC-signed audit trail",
        "Apache 2.0 core  ·  your infra",
    ]
    y = 580
    for b in bullets:
        draw.text((88, y), ">", font=sub, fill=FG)
        draw.text((128, y), b, font=sub, fill=DIM)
        y += 44

    url_f = load_font(REG, 24)
    draw.text((88, 960), "vaultrun.dev", font=url_f, fill=FG)
    return img


def video_endcard() -> Image.Image:
    w, h = 1920, 1080
    img = Image.new("RGBA", (w, h), BG)
    img = apply_glow(img, [(240, 80, 420, 22)])
    draw = ImageDraw.Draw(img)
    draw_grid(draw, w, h, 60, (22, 22, 22, 255))
    mark_size = 140
    draw_mark(draw, 160, 280, mark_size, FG)
    word = load_font(BOLD, 64)
    draw_spaced(draw, (160 + mark_size + 36, 318), "VAULTRUN", word, FG, tracking=8)
    sub = load_font(REG, 32)
    draw.text((160, 480), "Self-hosted secure runtime for AI agents", font=sub, fill=DIM)
    url_f = load_font(REG, 28)
    lines = [
        "vaultrun.dev",
        "github.com/nickvd7/vaultrun",
        "mail@030.dev",
    ]
    y = 620
    for line in lines:
        draw.text((160, y), line, font=url_f, fill=FG if line.startswith("vaultrun") else FAINT)
        y += 48
    return img


def _write_cover_preview() -> None:
    """Desktop mock (~1128×191 cover + logo overlap). Not for upload — QA only."""
    cover = Image.open(OUT / "linkedin-cover.png").convert("RGBA")
    disp_w, disp_h = 1128, 191
    cover_d = cover.resize((disp_w, disp_h), Image.Resampling.LANCZOS)
    logo = Image.open(OUT / "linkedin-logo.png").convert("RGBA").resize((88, 88), Image.Resampling.LANCZOS)
    canvas = Image.new("RGBA", (disp_w, disp_h + 52), (255, 255, 255, 255))
    canvas.paste(cover_d, (0, 0))
    # Page logo sits on the bottom edge, half overlapping the cover.
    canvas.paste(logo, (28, disp_h - 36), logo)
    path = Path("/tmp/linkedin-cover-preview.png")
    canvas.convert("RGB").save(path, "PNG", optimize=True)
    print(f"  preview (not uploaded) → {path}")


def main() -> None:
    OUT.mkdir(parents=True, exist_ok=True)
    print(f"Writing brand PNGs → {OUT}")

    save(mark_square(512), "mark.png")
    save(mark_square(512, light=True), "mark-light.png")
    save(lockup(1260, 192), "lockup.png")
    save(lockup(1260, 192, light=True), "lockup-light.png")

    save(mark_square(32), "favicon-32.png")
    save(mark_square(64), "favicon-64.png")
    save(mark_square(180), "apple-touch-icon.png")
    save(mark_square(192), "icon-192.png")
    save(mark_square(512), "icon-512.png")

    save(og_image(), "og.png")
    save(social_square(), "social-square.png")
    save(video_endcard(), "video-endcard.png")

    save(linkedin_logo(), "linkedin-logo.png")
    cover = linkedin_cover()
    save(cover, "linkedin-cover.png")
    save_jpeg(cover, "linkedin-cover.jpg")
    save(linkedin_profile_banner(), "linkedin-profile-banner.png")
    save(first_post(), "linkedin-first-post.png")

    _write_cover_preview()

    # LinkedIn 3 MB cap — warn if anything is oversized
    for name in (
        "linkedin-logo.png",
        "linkedin-cover.png",
        "linkedin-cover.jpg",
        "linkedin-profile-banner.png",
        "linkedin-first-post.png",
        "og.png",
    ):
        n = (OUT / name).stat().st_size
        if n > 3 * 1024 * 1024:
            print(f"WARNING: {name} is {n / 1024 / 1024:.2f} MB (LinkedIn cap is 3 MB)")


if __name__ == "__main__":
    main()
