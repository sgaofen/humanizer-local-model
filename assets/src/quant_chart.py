#!/usr/bin/env python3
"""Quantization chart for the README and the Hugging Face cards.

One chart: top-1 agreement with bf16 (llama.cpp "Same top p") against file size, two lines:
our released GGUF files, and plain llama.cpp quantization (llama-quantize + imatrix).
English and Chinese are averaged 50/50; both languages have the same number of scored tokens,
so this equals the token-weighted mean. Every point is a measured value from assets/data/quant-top1.csv.

    python3 assets/src/quant_chart.py --plot     # plot area for assets/src/quant.html + assets/quant-top1-{en,zh}.svg;
                                                 # then assets/src/render.py renders the card PNG (quant-top1-{en,zh}.png)
    python3 assets/src/quant_chart.py            # the older standalone matplotlib chart (title and footnote drawn by matplotlib)

Needs matplotlib and fontTools (with brotli, to read the app's woff2 fonts). Chinese text uses
PingFang SC (macOS) or Hiragino Sans GB.
"""
import csv
import glob
import os
import sys
import tempfile
from pathlib import Path

import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt
from matplotlib import font_manager
from matplotlib.lines import Line2D

ROOT = Path(__file__).resolve().parents[2]
DATA = ROOT / "assets" / "data" / "quant-top1.csv"
FONTS = ROOT / "app" / "web" / "fonts"
CACHE = Path(tempfile.gettempdir()) / "humanizer-chart-fonts"

# Colours: only our line carries a hue (a darker step of the brand lime that holds 3:1 on white);
# the standard line is neutral gray, as in the other README charts.
OURS = "#4F8F00"
OURS_TINT = "#F1F8E4"
STD = "#8A919C"
INK = "#14171C"
INK_2 = "#4A515C"
INK_3 = "#7A818C"
GRID = "#E7E9EC"
SURFACE = "#FFFFFF"

TEXT = {
    "en": dict(
        title="humanizer 12B GGUF: top-1 agreement with bf16",
        sub="How often each file's most likely next token is the same as the full bf16 model's. English and Chinese averaged. Higher is better.",
        x="File size (GB)",
        y="Top-1 agreement with bf16 (%)",
        ours="humanizer GGUF files (QAT = quantization-aware trained + distilled)",
        std="Standard llama.cpp quantization (llama-quantize + imatrix)",
        gap="+17 points\nat the same size, 3.9 GB",
        ref="bf16 = 100",
        foot=("llama.cpp llama-perplexity --kl-divergence (\"Same top p\"), same bf16 reference and the same ~30,700 held-out tokens per language.\n"
              "English and Chinese have equal token counts, so the plain average is also the token-weighted one. "
              "Standard builds: the importance matrix our files started from, default settings."),
        qat=" QAT",
    ),
    "zh": dict(
        title="humanizer 12B GGUF：与 bf16 的首选词一致率",
        sub="量化文件认为最可能的下一个词，和完整 bf16 模型相同的比例。中英文取平均，越高越好。",
        x="文件大小（GB）",
        y="与 bf16 首选词一致（%）",
        ours="humanizer GGUF 文件（QAT = 量化感知训练 + 蒸馏）",
        std="普通 llama.cpp 量化（llama-quantize + imatrix）",
        gap="高 17 个百分点\n同样 3.9 GB",
        ref="bf16 = 100",
        foot=("llama.cpp llama-perplexity --kl-divergence 的 “Same top p”；同一个 bf16 基准，每种语言同样约 30,700 个留出 token。\n"
              "中英文 token 数相同，直接平均就等于按 token 加权。普通量化用的是我们的文件起步时同一个 imatrix，其余参数默认。"),
        qat=" QAT",
    ),
}

# Label placement, in points from the marker: (dx, dy, ha). Ours above-left, standard below-right.
OFF_OURS = {
    "2-bit": (10, -9, "left"),
    "Q3": (-12, 12, "right"),
    "Q4_K_M": (-12, 13, "right"),
    "Q6_K": (-10, 14, "right"),
    "Q8_0": (-12, 13, "right"),
}
OFF_STD = {
    "IQ2_XXS": (12, -2, "left"),
    "IQ2_XS": (12, -6, "left"),
    "IQ2_M": (-10, 11, "right"),
    "Q2_K": (13, -12, "left"),
    "IQ3_XXS": (12, -6, "left"),
    "IQ3_M": (12, -9, "left"),
    "IQ4_XS": (12, -14, "left"),
    "Q4_K_M": (12, -16, "left"),
    "Q5_K_M": (12, -16, "left"),
    "Q6_K": (12, -16, "left"),
    "Q8_0": (-10, -16, "right"),
}


def _instance(woff2, wght, out):
    from fontTools.ttLib import TTFont
    from fontTools.varLib.instancer import instantiateVariableFont
    f = TTFont(woff2)
    f.flavor = None
    inst = instantiateVariableFont(f, {"wght": wght}, inplace=False, updateFontNames=True)
    inst.save(out)


def _ttc_face(ttc, want, out):
    from fontTools.ttLib import TTCollection
    for face in TTCollection(ttc).fonts:
        names = {n.toUnicode() for n in face["name"].names if n.nameID in (4, 6)}
        if want in names:
            face.save(out)
            return True
    return False


def setup_fonts():
    """Static instances of the app's fonts (Instrument Sans, JetBrains Mono) plus a CJK face."""
    CACHE.mkdir(parents=True, exist_ok=True)
    want = {
        "InstrumentSans-Regular.ttf": (FONTS / "InstrumentSans-Variable.woff2", 400),
        "InstrumentSans-SemiBold.ttf": (FONTS / "InstrumentSans-Variable.woff2", 600),
        "JetBrainsMono-Regular.ttf": (FONTS / "JetBrainsMono-Variable.woff2", 400),
        "JetBrainsMono-Medium.ttf": (FONTS / "JetBrainsMono-Variable.woff2", 500),
    }
    for name, (src, w) in want.items():
        out = CACHE / name
        if not out.exists():
            _instance(str(src), w, str(out))
        font_manager.fontManager.addfont(str(out))
    cjk = None
    pingfang = sorted(glob.glob("/System/Library/AssetsV2/com_apple_MobileAsset_Font*/*/AssetData/PingFang.ttc"))
    if pingfang:
        ok = all(_ttc_face(pingfang[0], face, str(CACHE / f"{face}.ttf")) or (CACHE / f"{face}.ttf").exists()
                 for face in ("PingFangSC-Regular", "PingFangSC-Semibold"))
        if ok:
            for face in ("PingFangSC-Regular", "PingFangSC-Semibold"):
                font_manager.fontManager.addfont(str(CACHE / f"{face}.ttf"))
            cjk = "PingFang SC"
    if cjk is None and os.path.exists("/System/Library/Fonts/Hiragino Sans GB.ttc"):
        font_manager.fontManager.addfont("/System/Library/Fonts/Hiragino Sans GB.ttc")
        cjk = "Hiragino Sans GB"
    return cjk


def load():
    rows = [r for r in csv.DictReader(open(DATA, encoding="utf-8")) if r["plotted"] == "yes"]
    for r in rows:
        r["x"] = int(r["bytes"]) / 1e9
        r["y"] = (float(r["top1_en"]) + float(r["top1_zh"])) / 2
    ours = sorted([r for r in rows if r["line"] == "ours"], key=lambda r: r["x"])
    std = sorted([r for r in rows if r["line"] == "standard"], key=lambda r: r["x"])
    return ours, std


PLOT_ONLY = False  # --plot: only the plot area, transparent, light "paper" palette, for assets/src/quant.html


def label(ax, r, text, dx, dy, ha, mine, mono):
    std_edge = "#DEDCD3" if PLOT_ONLY else "#C9CDD3"
    ax.annotate(
        text, (r["x"], r["y"]), xytext=(dx, dy), textcoords="offset points", ha=ha, va="center",
        fontsize=9.2 if mine else 8.6, fontfamily=mono, fontweight=500 if mine else 400,
        color=INK if mine else INK_2, zorder=6,
        bbox=dict(boxstyle="round,pad=0.28,rounding_size=0.25", fc=OURS_TINT if mine else SURFACE,
                  ec=OURS if mine else std_edge, lw=0.9),
        arrowprops=dict(arrowstyle="-", color=OURS if mine else "#B5BAC2", lw=0.8, shrinkA=0, shrinkB=4.5),
    )


def draw(lang, cjk):
    t = TEXT[lang]
    sans = ["Instrument Sans"] + ([cjk] if cjk else [])
    mono = ["JetBrains Mono"] + ([cjk] if cjk else [])
    plt.rcParams.update({
        "font.family": sans, "font.size": 11, "svg.fonttype": "path",
        "axes.edgecolor": "#C9CDD3", "axes.labelcolor": INK_2, "xtick.color": INK_3, "ytick.color": INK_3,
        "axes.unicode_minus": False,
    })
    ours, std = load()

    if PLOT_ONLY:
        fig = plt.figure(figsize=(10.2, 5.5), dpi=240)
        fig.patch.set_alpha(0)
        ax = fig.add_axes([0.072, 0.115, 0.918, 0.87])
        ax.set_facecolor("none")
    else:
        fig = plt.figure(figsize=(10, 6.3), dpi=240, facecolor=SURFACE)
        ax = fig.add_axes([0.075, 0.17, 0.905, 0.65])
        ax.set_facecolor(SURFACE)

    # Frame: hairline solid grid, left and bottom spines only.
    ax.set_xlim(3.2, 13.3)
    ax.set_ylim(54, 101.2)
    ax.set_xticks(range(4, 14))
    ax.set_yticks(range(55, 101, 5))
    ax.grid(True, color=GRID, lw=0.8, zorder=0)
    ax.tick_params(length=0, labelsize=10, pad=6)
    for s in ("top", "right"):
        ax.spines[s].set_visible(False)
    for s in ("left", "bottom"):
        ax.spines[s].set_color("#C9CDD3")
        ax.spines[s].set_linewidth(0.8)
    ax.set_xlabel(t["x"], fontsize=10.5, labelpad=8)
    ax.set_ylabel(t["y"], fontsize=10.5, labelpad=8)

    # bf16 reference line.
    ax.axhline(100, color="#AEB4BD", lw=0.9, zorder=1)
    ax.text(3.27, 100.3, t["ref"], ha="left", va="bottom", fontsize=8.6, fontfamily=mono, color=INK_3)

    # Lines: 2.2 px, round joins; markers with a white ring.
    ax.plot([r["x"] for r in std], [r["y"] for r in std], color=STD, lw=2.0, solid_joinstyle="round",
            solid_capstyle="round", zorder=3)
    ax.scatter([r["x"] for r in std], [r["y"] for r in std], s=46, color=STD, edgecolors=SURFACE, linewidths=1.6, zorder=4)
    ax.plot([r["x"] for r in ours], [r["y"] for r in ours], color=OURS, lw=2.6, solid_joinstyle="round",
            solid_capstyle="round", zorder=5)
    ax.scatter([r["x"] for r in ours], [r["y"] for r in ours], s=64, color=OURS, edgecolors=SURFACE, linewidths=1.8, zorder=6)

    for r in std:
        dx, dy, ha = OFF_STD[r["label"]]
        label(ax, r, r["label"], dx, dy, ha, False, mono)
    for r in ours:
        dx, dy, ha = OFF_OURS[r["label"]]
        text = r["label"] + (t["qat"] if r["qat"] == "yes" else "")
        label(ax, r, text, dx, dy, ha, True, mono)

    # The one call-out: same size, 2-bit.
    o2 = next(r for r in ours if r["label"] == "2-bit")
    s2 = next(r for r in std if r["label"] == "IQ2_XS")
    bx = 3.6
    ax.annotate("", xy=(bx, o2["y"] - 0.3), xytext=(bx, s2["y"] + 0.3),
                arrowprops=dict(arrowstyle="<->,head_length=0.45,head_width=0.22", color=INK_2, lw=1.0), zorder=5)
    for yy in (o2["y"], s2["y"]):
        ax.plot([bx, o2["x"] - 0.07], [yy, yy], color="#C9CDD3", lw=0.8, ls=(0, (2, 2)), zorder=2)
    big, small = t["gap"].split("\n")
    ax.text(bx - 0.33, o2["y"] + 8.3, big, ha="left", va="baseline", fontsize=13, fontweight=600, color=INK, zorder=6)
    ax.text(bx - 0.33, o2["y"] + 5.4, small, ha="left", va="baseline", fontsize=9.5, color=INK_2, zorder=6)

    # Legend (bottom right, where the plot is empty).
    handles = [
        Line2D([0], [0], color=OURS, lw=2.6, marker="o", ms=7.5, mfc=OURS, mec=SURFACE, mew=1.6),
        Line2D([0], [0], color=STD, lw=2.0, marker="o", ms=6.5, mfc=STD, mec=SURFACE, mew=1.4),
    ]
    leg = ax.legend(handles, [t["ours"], t["std"]], loc="lower right", bbox_to_anchor=(1.0, 0.02), frameon=True,
                    fontsize=10, handlelength=2.6, borderpad=0.8, labelspacing=0.7, framealpha=1)
    leg.get_frame().set_edgecolor("#DADDE1")
    leg.get_frame().set_linewidth(0.8)
    leg.get_frame().set_boxstyle("round,pad=0.2,rounding_size=0.4")
    for txt in leg.get_texts():
        txt.set_color(INK)

    if PLOT_ONLY:
        out = ROOT / "assets" / "src" / f"_quant-plot-{lang}.svg"
        fig.savefig(str(out), transparent=True)
        pub = ROOT / "assets" / f"quant-top1-{lang}.svg"   # 对外的 SVG:同一张图,白底
        fig.savefig(str(pub), facecolor="white")
        print("wrote", pub.relative_to(ROOT))
        plt.close(fig)
        print("wrote", out.relative_to(ROOT))
        return

    # Title, subtitle, footnote.
    fig.text(0.075, 0.935, t["title"], fontsize=16.5, fontweight=600, color=INK, ha="left", va="baseline")
    fig.text(0.075, 0.885, t["sub"], fontsize=10.5, color=INK_2, ha="left", va="baseline")
    fig.text(0.075, 0.03, t["foot"], fontsize=8.0, color=INK_3, ha="left", va="bottom", linespacing=1.45)

    out = ROOT / "assets" / f"quant-top1-{lang}"
    fig.savefig(str(out) + ".png", dpi=240, facecolor=SURFACE)
    fig.savefig(str(out) + ".svg", facecolor=SURFACE)
    plt.close(fig)
    print("wrote", out.relative_to(ROOT), ".png/.svg")


def main():
    global PLOT_ONLY, GRID, STD, OURS_TINT
    cjk = setup_fonts()
    args = sys.argv[1:]
    if "--plot" in args:
        PLOT_ONLY = True
        GRID, STD, OURS_TINT = "#EFEEE8", "#A3A9B1", "#EDFAC0"
        args = [a for a in args if a != "--plot"]
    langs = args or ["en", "zh"]
    for lang in langs:
        if lang == "zh" and not cjk:
            sys.exit("no CJK font found for the Chinese chart")
        draw(lang, cjk)


if __name__ == "__main__":
    main()
