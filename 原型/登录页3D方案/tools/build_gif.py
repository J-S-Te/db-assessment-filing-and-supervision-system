#!/usr/bin/env python3
"""把捕获的 PNG 帧序列合成 GIF（全局调色板 + 帧间差分 + 降采样）"""
import argparse, glob, os, sys
from PIL import Image, ImageFilter


def build(frames_dir, out_path, width, fps, colors, loop, dither, seconds, blur):
    files = sorted(glob.glob(os.path.join(frames_dir, '*.png')))
    if not files:
        sys.exit(f'没有帧: {frames_dir}')
    frames = [Image.open(f).convert('RGB') for f in files]

    # 按目标时长降采样
    if seconds:
        want = max(1, round(seconds * fps))
        if want < len(frames):
            step = len(frames) / want
            idx = [min(len(frames) - 1, int(i * step)) for i in range(want)]
            frames = [frames[i] for i in idx]

    w0, h0 = frames[0].size
    if width and width < w0:
        h = round(h0 * width / w0)
        frames = [f.resize((width, h), Image.LANCZOS) for f in frames]
    w, h = frames[0].size

    if blur:
        frames = [f.filter(ImageFilter.GaussianBlur(blur)) for f in frames]

    # 用若干帧拼图生成统一调色板
    picks = [frames[i] for i in range(0, len(frames), max(1, len(frames) // 6))][:6]
    mosaic = Image.new('RGB', (w, h * len(picks)))
    for i, f in enumerate(picks):
        mosaic.paste(f, (0, i * h))
    master = mosaic.quantize(colors=colors, method=Image.MEDIANCUT)

    d = Image.Dither.NONE if not dither else Image.Dither.FLOYDSTEINBERG
    conv = [f.quantize(palette=master, dither=d) for f in frames]

    conv[0].save(out_path, save_all=True, append_images=conv[1:],
                 duration=int(round(1000 / fps)), loop=loop, optimize=True, disposal=2)
    print(f'{os.path.basename(out_path)}  {w}x{h}  {len(conv)} 帧  '
          f'{os.path.getsize(out_path)/1024/1024:.2f} MB')


if __name__ == '__main__':
    p = argparse.ArgumentParser()
    p.add_argument('frames_dir')
    p.add_argument('out')
    p.add_argument('--width', type=int, default=960)
    p.add_argument('--fps', type=int, default=12)
    p.add_argument('--colors', type=int, default=96)
    p.add_argument('--loop', type=int, default=0)
    p.add_argument('--dither', action='store_true')
    p.add_argument('--seconds', type=float, default=None)
    p.add_argument('--blur', type=float, default=0)
    a = p.parse_args()
    build(a.frames_dir, a.out, a.width, a.fps, a.colors, a.loop, a.dither, a.seconds, a.blur)
