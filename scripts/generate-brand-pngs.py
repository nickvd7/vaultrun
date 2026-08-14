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


def _vsub(a: tuple[float, float], b: tuple[float, float]) -> tuple[float, float]:
    return (a[0] - b[0], a[1] - b[1])


def _vadd(a: tuple[float, float], b: tuple[float, float]) -> tuple[float, float]:
    return (a[0] + b[0], a[1] + b[1])


def _vmul(a: tuple[float, float], s: float) -> tuple[float, float]:
    return (a[0] * s, a[1] * s)


def _vnorm(a: tuple[float, float]) -> tuple[float, float]:
    length = (a[0] ** 2 + a[1] ** 2) ** 0.5 or 1.0
    return (a[0] / length, a[1] / length)


def _vperp(a: tuple[float, float]) -> tuple[float, float]:
    return (-a[1], a[0])


def _intersect(
    p: tuple[float, float],
    r: tuple[float, float],
    q: tuple[float, float],
    s: tuple[float, float],
) -> tuple[float, float] | None:
    cross = r[0] * s[1] - r[1] * s[0]
    if abs(cross) < 1e-9:
        return None
    t = ((q[0] - p[0]) * s[1] - (q[1] - p[1]) * s[0]) / cross
    return _vadd(p, _vmul(r, t))


def _mitered_chevron(p0: tuple[float, float], p1: tuple[float, float], p2: tuple[float, float], width: float) -> list[tuple[float, float]]:
    """Stroke polygon for SVG path M p0 L p1 L p2 (square cap, miter join)."""
    hw = width / 2.0
    d1 = _vnorm(_vsub(p1, p0))
    d2 = _vnorm(_vsub(p2, p1))
    n1 = _vperp(d1)
    n2 = _vperp(d2)
    start = _vsub(p0, _vmul(d1, hw))
    end = _vadd(p2, _vmul(d2, hw))
    start_l = _vadd(start, _vmul(n1, hw))
    start_r = _vadd(start, _vmul(n1, -hw))
    end_l = _vadd(end, _vmul(n2, hw))
    end_r = _vadd(end, _vmul(n2, -hw))
    o1l = _vadd(p0, _vmul(n1, hw))
    o1r = _vadd(p0, _vmul(n1, -hw))
    o2l = _vadd(p1, _vmul(n2, hw))
    o2r = _vadd(p1, _vmul(n2, -hw))
    miter_l = _intersect(o1l, d1, o2l, d2) or _vadd(p1, _vmul(n1, hw))
    miter_r = _intersect(o1r, d1, o2r, d2) or _vadd(p1, _vmul(n1, -hw))
    return [start_l, miter_l, end_l, end_r, miter_r, start_r]


def draw_mark(draw: ImageDraw.ImageDraw, x: float, y: float, size: float, fg, *, stroke_scale: float | None = None) -> None:
    """Terminal prompt >_ inside a square frame (SVG viewBox 64).

    Chevron is a mitered stroke polygon (not two overlapping PIL lines) so the
    tip stays sharp.
    """
    s = size / 64.0
    frame_sw = (2.5 if stroke_scale is None else stroke_scale) * s
    half = frame_sw / 2.0
    o0x, o0y = x + 8 * s - half, y + 8 * s - half
    o1x, o1y = x + 56 * s + half, y + 56 * s + half
    i0x, i0y = x + 8 * s + half, y + 8 * s + half
    i1x, i1y = x + 56 * s - half, y + 56 * s - half
    draw.rectangle([o0x, o0y, o1x, i0y], fill=fg)
    draw.rectangle([o0x, i1y, o1x, o1y], fill=fg)
    draw.rectangle([o0x, o0y, i0x, o1y], fill=fg)
    draw.rectangle([i1x, o0y, o1x, o1y], fill=fg)

    chev = _mitered_chevron((20.0, 22.0), (34.0, 32.0), (20.0, 42.0), 3.2)
    # Push the outer miter a hair past the geometric join so PIL's polygon
    # rasterizer doesn't flatten the tip into a vertical stub.
    tip = chev[4]
    p1 = (34.0, 32.0)
    outward = _vnorm(_vsub(tip, p1))
    chev[4] = _vadd(tip, _vmul(outward, 0.55))
    pts = [(x + px * s, y + py * s) for px, py in chev]
    draw.polygon(pts, fill=fg)

    draw.rectangle([x + 38 * s, y + 28 * s, x + 48 * s, y + 36 * s], fill=fg)


def render_mark(size: int, *, light: bool = False, pad: int = 0) -> Image.Image:
    """Supersample 8× then Lanczos-down so diagonals stay sharp at logo sizes."""
    bg, fg = (WHITE, BLACK) if light else (BG, FG)
    inner = max(8, size - 2 * pad)
    ss = 8
    big = inner * ss
    img = Image.new("RGBA", (big, big), bg)
    draw = ImageDraw.Draw(img)
    draw_mark(draw, 0, 0, big, fg)
    small = img.resize((inner, inner), Image.Resampling.LANCZOS)
    small = small.filter(ImageFilter.UnsharpMask(radius=1.4, percent=160, threshold=1))
    if pad <= 0:
        return small
    out = Image.new("RGBA", (size, size), bg)
    out.paste(small, (pad, pad))
    return out


def paste_mark(img: Image.Image, xy: tuple[int, int], size: int) -> None:
    mark = render_mark(size)
    img.paste(mark, (int(xy[0]), int(xy[1])))


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
    return render_mark(size, light=light)


def lockup(width: int, height: int, *, light: bool = False) -> Image.Image:
    bg, fg = (WHITE, BLACK) if light else (BG, FG)
    img = Image.new("RGBA", (width, height), bg)
    mark = render_mark(height, light=light)
    img.paste(mark, (0, 0))
    draw = ImageDraw.Draw(img)
    f = load_font(BOLD, max(18, int(height * 0.44)))
    tracking = max(1.0, height * 0.04)
    tx = height + int(height * 0.12)
    ty = (height - f.size) / 2 - height * 0.06
    draw_spaced(draw, (tx, ty), "VAULTRUN", f, fg, tracking=tracking)
    return img


def linkedin_logo() -> Image.Image:
    """1024×1024 page logo. Square mark only, supersampled, padded for rounded crop."""
    return render_mark(1024, light=False, pad=64)


def linkedin_cover() -> Image.Image:
    """4200×700 company cover (6:1). Type sits in the RIGHT half; tagline is
    centered under VAULTRUN so it reads as one lockup. Rendered 2× then down."""
    scale = 2
    w, h = 4200 * scale, 700 * scale
    img = Image.new("RGBA", (w, h), BG)
    img = apply_glow(img, [(w * 0.62, -40 * scale, 820 * scale, 18)])
    draw = ImageDraw.Draw(img)
    draw_grid(draw, w, h, 70 * scale, (22, 22, 22, 255))
    draw.line([(0, 2 * scale), (w, 2 * scale)], fill=LINE, width=3 * scale)
    draw.line([(0, h - 3 * scale), (w, h - 3 * scale)], fill=LINE, width=3 * scale)

    word = load_font(BOLD, 200 * scale)
    tag = load_font(REG, 44 * scale)
    tracking = 16 * scale
    tag_line = "Self-hosted secure runtime for AI agents"
    word_w = text_width(draw, "VAULTRUN", word, tracking)
    tag_w = draw.textlength(tag_line, font=tag)
    block_w = max(word_w, tag_w)
    tx_block = w - 560 * scale - int(block_w)
    tx_word = tx_block + (block_w - word_w) / 2
    tx_tag = tx_block + (block_w - tag_w) / 2
    gap = 28 * scale
    block_h = 200 * scale + gap + 44 * scale
    ty = (h - block_h) // 2
    draw_spaced(draw, (tx_word, ty), "VAULTRUN", word, FG, tracking=tracking)
    draw.text((tx_tag, ty + 200 * scale + gap), tag_line, font=tag, fill=(196, 196, 196, 255))
    return img.resize((4200, 700), Image.Resampling.LANCZOS)


def linkedin_profile_banner() -> Image.Image:
    """1584×396 personal-profile banner (founder)."""
    w, h = 1584, 396
    img = Image.new("RGBA", (w, h), BG)
    img = apply_glow(img, [(180, -20, 280, 26), (1400, 200, 260, 14)])
    draw = ImageDraw.Draw(img)
    draw_grid(draw, w, h, 44, (22, 22, 22, 255))
    mark_size = 168
    mx, my = 72, (h - mark_size) // 2
    paste_mark(img, (mx, my), mark_size)
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
    paste_mark(img, (80, 72), mark_size)
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
    paste_mark(img, ((w - mark_size) // 2, 160), mark_size)
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
    paste_mark(img, (80, 160), mark_size)
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
    paste_mark(img, (160, 280), mark_size)
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
