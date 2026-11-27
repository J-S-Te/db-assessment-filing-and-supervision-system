#!/usr/bin/env python3
"""把项目里的区县 GeoJSON 离线投影成 SVG path，供驾驶舱设计稿内联使用。

用法（在仓库根目录运行）：
    python3 原型/驾驶舱改版/tools/project-map.py \
        grade-business/web/src/assets/district-boundaries.geojson \
        /tmp/qz_paths.json

投影方式与前端 `web/src/lib/dashboardGeo.ts` 的
`geoIdentity().reflectY(true).fitExtent([[16,16],[540,410]], geometry)` 一致：
等距柱面投影 + Y 轴翻转 + 等比适配到视窗，四角留 16 单位内边距。
输出 JSON 含 `viewBox`（区县实际包围盒 + 边距）与各区县的
`rings`（投影后的二维环坐标，供三维渲染层做真实透视计算）与 `path` / 标签质心。
"""
from __future__ import annotations

import json
import re
import sys
from pathlib import Path

VIEW_W, VIEW_H, PAD = 560.0, 430.0, 20.0
POINT = re.compile(r"[ML]\s*(-?\d+\.?\d*)\s+(-?\d+\.?\d*)")


def check_path(path: str, suffixes: tuple[str, ...]) -> Path:
    parts = re.split(r"[\\/]+", path)
    if ".." in parts:
        raise SystemExit(f"路径不允许包含 .. 目录分量：{path}")
    if not path.lower().endswith(suffixes):
        raise SystemExit(f"路径须以 {' / '.join(suffixes)} 结尾：{path}")
    return Path(path)


def project(geojson: dict) -> dict:
    items = []
    for feature in geojson["features"]:
        geom = feature["geometry"]
        polygons = [geom["coordinates"]] if geom["type"] == "Polygon" else geom["coordinates"]
        # reflectY(true)：经纬度直接当平面坐标，Y 取负
        items.append((feature["properties"]["name"],
                      [[[(p[0], -p[1]) for p in ring] for ring in poly] for poly in polygons]))

    xs = [p[0] for _, polys in items for rings in polys for ring in rings for p in ring]
    ys = [p[1] for _, polys in items for rings in polys for ring in rings for p in ring]
    min_x, max_x, min_y, max_y = min(xs), max(xs), min(ys), max(ys)
    scale = min((VIEW_W - PAD * 2) / (max_x - min_x), (VIEW_H - PAD * 2) / (max_y - min_y))
    tx = (PAD + VIEW_W - PAD) / 2 - scale * (min_x + max_x) / 2
    ty = (PAD + VIEW_H - PAD) / 2 - scale * (min_y + max_y) / 2

    def to_xy(x: float, y: float) -> tuple[float, float]:
        return (round(scale * x + tx, 2), round(scale * y + ty, 2))

    def ring_path(ring: list[tuple[float, float]]) -> str:
        parts = []
        for index, (x, y) in enumerate(ring):
            px, py = to_xy(x, y)
            parts.append(("M" if index == 0 else "L") + f"{px} {py}")
        return " ".join(parts) + " Z"

    def centroid(ring: list[tuple[float, float]]) -> tuple[float, float] | None:
        area = cx = cy = 0.0
        count = len(ring)
        for index in range(count):
            x1, y1 = ring[index]
            x2, y2 = ring[(index + 1) % count]
            cross = x1 * y2 - x2 * y1
            area += cross
            cx += (x1 + x2) * cross
            cy += (y1 + y2) * cross
        area *= 0.5
        if area == 0:
            return None
        return (cx / (6 * area), cy / (6 * area))

    districts = []
    all_x: list[float] = []
    all_y: list[float] = []
    for name, polygons in items:
        paths = []
        best: tuple[float, float] | None = None
        best_area = -1.0
        for rings in polygons:
            for ring in rings:
                paths.append(ring_path(ring))
            exterior = rings[0]
            shoelace = 0.0
            for index in range(len(exterior)):
                ax, ay = exterior[index]
                bx, by = exterior[(index + 1) % len(exterior)]
                shoelace += ax * by - bx * ay
            polygon_area = abs(shoelace) / 2
            if polygon_area > best_area:
                best_area = polygon_area
                best = centroid(exterior)
        lx, ly = to_xy(*best) if best else (0.0, 0.0)
        rings_2d = []
        for rings in polygons:
            rings_2d.append([[list(to_xy(x, y)) for x, y in ring] for ring in rings])
        districts.append({"name": name, "d": " ".join(paths), "lx": lx, "ly": ly,
                          "rings": rings_2d})
        for path in paths:
            for match in POINT.finditer(path):
                all_x.append(float(match.group(1)))
                all_y.append(float(match.group(2)))

    margin = 6
    view = [round(min(all_x) - margin, 2), round(min(all_y) - margin, 2),
            round(max(all_x) - min(all_x) + margin * 2, 2),
            round(max(all_y) - min(all_y) + margin * 2, 2)]
    return {"viewBox": " ".join(str(v) for v in view), "districts": districts}


def main() -> None:
    if len(sys.argv) != 3:
        print(__doc__)
        raise SystemExit(2)
    source = check_path(sys.argv[1], (".geojson", ".json"))
    target = check_path(sys.argv[2], (".json",))
    geojson = json.loads(source.read_text(encoding="utf-8"))
    result = project(geojson)
    target.write_text(json.dumps(result, ensure_ascii=False, indent=1), encoding="utf-8")
    print("viewBox =", result["viewBox"])
    for district in result["districts"]:
        print(f"  {district['name']}: label=({district['lx']},{district['ly']})")


if __name__ == "__main__":
    main()
