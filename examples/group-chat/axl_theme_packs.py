"""
AXL theme packs — Frutiger Aero / Aqua + Windows 95 structure.

Defaults are intentionally soft: sky whites, steel/aqua blues, silver chrome.
Avoid harsh neons; use Ctrl+T or Ctrl+P → Theme to switch packs.
"""

from __future__ import annotations

from textual.app import App
from textual.theme import Theme

# ── Frutiger Aero / Aqua (default): calm sky, glassy chrome, readable blue-slate text ──

_FOOTER_AERO = {
    "footer-key-foreground": "#4A7AA8",
    "footer-description-foreground": "#3D5A78",
    "footer-foreground": "#3D5A78",
    "footer-background": "#D8E8F4",
    "footer-key-background": "transparent",
    # Textual Input uses $border / $border-blurred for focus rings; pin to blues (no green tint).
    "border": "#4A6FA0",
    "border-blurred": "#A8B8C8",
}

AXL_THEME_AERO_LIGHT = Theme(
    name="axl-aero-light",
    primary="#4A6FA0",
    secondary="#7BA3C8",
    accent="#5E9BC4",
    warning="#C4A050",
    error="#B85C6A",
    # Steel / blue slate — avoid green-adjacent teals for “success” (terminals map them to neon green).
    success="#5A7FA0",
    foreground="#2D4A62",
    background="#EBF4FA",
    surface="#F5FAFE",
    panel="#DFEDF6",
    boost="#E8F2FA",
    dark=False,
    variables={
        **_FOOTER_AERO,
        "block-cursor-background": "#7BA3C8",
        "block-cursor-foreground": "#F5FAFE",
        "input-selection-background": "#5E9BC4 28%",
    },
)

# Softer “mall” variants — lavender hints, no hot pink
_FOOTER_MALL_SOFT = {
    "footer-key-foreground": "#6B7FA8",
    "footer-description-foreground": "#3E4D68",
    "footer-foreground": "#3E4D68",
    "footer-background": "#E4E8F2",
    "border": "#5A6A98",
    "border-blurred": "#B8B8C8",
}

AXL_THEME_MALL_LIGHT = Theme(
    name="axl-mall-light",
    primary="#4A5688",
    secondary="#A8B0D0",
    accent="#B8B8E0",
    warning="#C9A050",
    error="#B86A78",
    success="#6B7FA8",
    foreground="#3A3F58",
    background="#F0EDF6",
    surface="#FAF8FC",
    panel="#EAE6F2",
    boost="#E8E4F0",
    dark=False,
    variables={
        **_FOOTER_MALL_SOFT,
        "block-cursor-background": "#A8B0D0",
        "block-cursor-foreground": "#3A3F58",
    },
)

_FOOTER_MALL_DARK = {
    "footer-key-foreground": "#8BB8D8",
    "footer-description-foreground": "#B0D0E8",
    "footer-foreground": "#B8D8E8",
    "footer-background": "#141C28",
    "border": "#7BA3C8",
    "border-blurred": "#4A5A68",
}

AXL_THEME_MALL_DARK = Theme(
    name="axl-mall-dark",
    primary="#7BA3C8",
    secondary="#6BA0B8",
    accent="#8BC4E0",
    warning="#D4B060",
    error="#D08090",
    success="#6BA0C8",
    foreground="#B8D5EA",
    background="#101820",
    surface="#161F2C",
    panel="#1C2838",
    boost="#243040",
    dark=True,
    variables={
        **_FOOTER_MALL_DARK,
        "block-cursor-background": "#8BC4E0",
        "block-cursor-foreground": "#101820",
    },
)

_FOOTER_CHROME = {
    "footer-key-foreground": "#3D6A9E",
    "footer-description-foreground": "#3A4A62",
    "footer-foreground": "#3A4A62",
    "footer-background": "#D0DCE8",
    "border": "#3D6488",
    "border-blurred": "#A8B0C0",
}

AXL_THEME_AQUA_CHROME = Theme(
    name="axl-aqua-chrome",
    primary="#3D6488",
    secondary="#6B94B8",
    accent="#6EB0D4",
    warning="#C9A050",
    error="#B85C6A",
    success="#5A7FA0",
    foreground="#2E4055",
    background="#E6EEF6",
    surface="#F4F8FC",
    panel="#D8E4F0",
    boost="#E0EAF4",
    dark=False,
    variables={
        **_FOOTER_CHROME,
        "block-cursor-background": "#6EB0D4",
        "block-cursor-foreground": "#F4F8FC",
    },
)

_FOOTER_HOLO = {
    "footer-key-foreground": "#7088A8",
    "footer-description-foreground": "#404858",
    "footer-foreground": "#404858",
    "footer-background": "#E2E6F0",
    "border": "#7088A8",
    "border-blurred": "#B8C0D0",
}

AXL_THEME_HOLO_SILVER = Theme(
    name="axl-holo-silver",
    primary="#8898B8",
    secondary="#A8B8D0",
    accent="#A8C0D8",
    warning="#C9A050",
    error="#B86A78",
    success="#6B8AA8",
    foreground="#3A4558",
    background="#EEF0F6",
    surface="#F6F8FC",
    panel="#E4E8F2",
    boost="#ECEEF5",
    dark=False,
    variables={
        **_FOOTER_HOLO,
        "block-cursor-background": "#A8C0D8",
        "block-cursor-foreground": "#F6F8FC",
    },
)

AXL_THEMES: tuple[Theme, ...] = (
    AXL_THEME_AERO_LIGHT,
    AXL_THEME_AQUA_CHROME,
    AXL_THEME_HOLO_SILVER,
    AXL_THEME_MALL_LIGHT,
    AXL_THEME_MALL_DARK,
)

DEFAULT_AXL_THEME_NAME = "axl-aero-light"


def register_axl_themes(app: App) -> None:
    """Register all AXL theme packs on the app (safe to call once at startup)."""
    for theme in AXL_THEMES:
        app.register_theme(theme)
