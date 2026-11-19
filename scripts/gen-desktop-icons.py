#!/usr/bin/env python3
"""Generate the FlowDrop desktop icon set (icon.png, tray.png, installer set).

The desktop shell used a bare blue rounded square for both the application
icon and the tray icon, while the Web favicon and the Android launcher already
carry the brand mark: two white arrows (transfer both ways) on blue. This
script draws that mark once and renders the two files the desktop embeds:

  desktop/assets/icon.png   512x512, application/window/exe icon source
  desktop/assets/tray.png    48x48,  tray (also used by Windows notifications)
  deploy/desktop/flowdrop-{48,128,256}.png
                             what scripts/install-desktop.sh puts in
                             ~/.local/share/icons/hicolor, i.e. the launcher and
                             window/taskbar icon on Linux

It is deterministic (no fonts, no randomness) and only needs Pillow:

    python3 scripts/gen-desktop-icons.py

Shapes are the Android launcher foreground geometry (108-unit viewport,
arrows at y 35..73), scaled up so the mark stays readable at 16 px.
"""

from pathlib import Path

from PIL import Image, ImageDraw

BLUE = (37, 99, 235, 255)  # #2563EB, same as web/public/favicon.svg
WHITE = (255, 255, 255, 255)
SUPERSAMPLE = 8

ROOT = Path(__file__).resolve().parent.parent
ICON_PATH = ROOT / "desktop" / "assets" / "icon.png"
TRAY_PATH = ROOT / "desktop" / "assets" / "tray.png"
DEPLOY_DIR = ROOT / "deploy" / "desktop"

# Android adaptive-icon foreground paths, in its 108x108 viewport. Both arrows
# are white, so their overlapping tails merge into one mark.
RIGHT_ARROW = [(40, 46), (66, 46), (59, 39), (63, 35), (77, 49),
               (63, 63), (59, 59), (66, 52), (40, 52)]
LEFT_ARROW = [(68, 62), (42, 62), (49, 69), (45, 73), (31, 59),
              (45, 45), (49, 49), (42, 56), (68, 56)]
VIEWPORT = 108.0
CENTER = 54.0


def rounded_square(size: int, radius_ratio: float = 0.22) -> Image.Image:
    img = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    draw = ImageDraw.Draw(img)
    r = round(size * radius_ratio)
    draw.rounded_rectangle((0, 0, size - 1, size - 1), radius=r, fill=BLUE)
    return img


def render(size: int, mark_scale: float) -> Image.Image:
    """Draw the icon at `size`; mark_scale is relative to the 108 viewport."""
    big = size * SUPERSAMPLE
    img = rounded_square(big)
    draw = ImageDraw.Draw(img)
    k = big * mark_scale / VIEWPORT

    def project(points):
        return [((x - CENTER) * k + big / 2, (y - CENTER) * k + big / 2)
                for x, y in points]

    draw.polygon(project(RIGHT_ARROW), fill=WHITE)
    draw.polygon(project(LEFT_ARROW), fill=WHITE)
    return img.resize((size, size), Image.LANCZOS)


def main() -> None:
    icon = render(512, 1.18)
    tray = render(48, 1.65)
    icon.save(ICON_PATH, optimize=True)
    tray.save(TRAY_PATH, optimize=True)
    print(f"wrote {ICON_PATH} ({icon.width}x{icon.height})")
    print(f"wrote {TRAY_PATH} ({tray.width}x{tray.height})")
    # The installer ships these from deploy/; they used to be leftover flat
    # blue squares, so a fresh install showed a blank icon in the launcher and
    # the window even when the binary itself carried the mark.
    for size in (48, 128, 256):
        path = DEPLOY_DIR / f"flowdrop-{size}.png"
        render(size, 1.18).save(path, optimize=True)
        print(f"wrote {path} ({size}x{size})")


if __name__ == "__main__":
    main()
