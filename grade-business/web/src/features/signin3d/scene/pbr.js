import * as THREE from 'three-signin';

/**
 * PBR 材质工厂
 *
 * 这一层的思路：先画一张「高度图」（灰度），再用它推导出一整套
 * albedo / normal / roughness / metalness / ao 贴图。
 * 这样金属表面才会有真正的浮雕感（倒角、面板缝、拉丝），
 * 而不是一张贴图糊上去的假金属。
 */

/** 可复现随机数 */
export function makeRandom(seed = 20260821) {
  let s = seed >>> 0;
  return () => {
    s = (s * 1664525 + 1013904223) >>> 0;
    return s / 4294967296;
  };
}

/**
 * 用途 为主线程与贴图任务创建同尺寸的 2D 绘图画布。
 * 参数 w、h 为画布像素尺寸。
 * 返回值 当前环境对应的 Canvas 或 OffscreenCanvas。
 */
export function newCanvas(w, h) {
  if (typeof document === 'undefined') return new OffscreenCanvas(w, h);
  const canvas = document.createElement('canvas');
  canvas.width = w;
  canvas.height = h;
  return canvas;
}

function tex(canvas, { srgb = false, aniso = 16, repeat = null } = {}) {
  const t = new THREE.CanvasTexture(canvas);
  if (srgb) t.colorSpace = THREE.SRGBColorSpace;
  t.anisotropy = aniso;
  if (repeat) {
    t.wrapS = THREE.RepeatWrapping;
    t.wrapT = THREE.RepeatWrapping;
    t.repeat.set(repeat[0], repeat[1]);
  }
  t.needsUpdate = true;
  return t;
}

/**
 * 用途 用 Sobel 算子从高度图生成同分辨率的法线图。
 * 参数 heightCanvas 为灰度高度图，strength 为凹凸强度，flipY 控制绿通道翻转。
 * 返回值 包含原法线像素的画布。
 */
export function heightToNormal(heightCanvas, strength = 2.2, flipY = true) {
  const w = heightCanvas.width;
  const h = heightCanvas.height;
  const src = heightCanvas.getContext('2d').getImageData(0, 0, w, h).data;
  const out = newCanvas(w, h);
  const octx = out.getContext('2d');
  const img = octx.createImageData(w, h);

  const gray = Float64Array.from({ length: 256 }, (_, value) => value / 255);
  const pitch = w * 4;

  for (let y = 0; y < h; y++) {
    const row = y * pitch;
    const above = y === 0 ? (h - 1) * pitch : row - pitch;
    const below = y === h - 1 ? 0 : row + pitch;
    for (let x = 0; x < w; x++) {
      const column = x * 4;
      const left = x === 0 ? pitch - 4 : column - 4;
      const right = x === w - 1 ? 0 : column + 4;
      const tl = gray[src[above + left]];
      const t = gray[src[above + column]];
      const tr = gray[src[above + right]];
      const l = gray[src[row + left]];
      const r = gray[src[row + right]];
      const bl = gray[src[below + left]];
      const b = gray[src[below + column]];
      const br = gray[src[below + right]];

      const dx = (tr + 2 * r + br) - (tl + 2 * l + bl);
      const dy = (bl + 2 * b + br) - (tl + 2 * t + tr);

      let nx = -dx * strength;
      let ny = dy * strength * (flipY ? -1 : 1);
      let nz = 1;
      const len = Math.hypot(nx, ny, nz) || 1;
      nx /= len;
      ny /= len;
      nz /= len;

      const i = row + column;
      img.data[i] = (nx * 0.5 + 0.5) * 255;
      img.data[i + 1] = (ny * 0.5 + 0.5) * 255;
      img.data[i + 2] = (nz * 0.5 + 0.5) * 255;
      img.data[i + 3] = 255;
    }
  }
  octx.putImageData(img, 0, 0);
  return out;
}

/** 从高度图推 AO（越高越亮） */
function heightToAO(heightCanvas, gain = 1.5) {
  const w = heightCanvas.width;
  const h = heightCanvas.height;
  const src = heightCanvas.getContext('2d').getImageData(0, 0, w, h).data;
  const out = newCanvas(w, h);
  const octx = out.getContext('2d');
  const img = octx.createImageData(w, h);
  for (let i = 0; i < src.length; i += 4) {
    const v = Math.max(0, Math.min(1, 0.55 + (src[i] / 255 - 0.5) * gain));
    img.data[i] = img.data[i + 1] = img.data[i + 2] = v * 255;
    img.data[i + 3] = 255;
  }
  octx.putImageData(img, 0, 0);
  return out;
}

/**
 * 程序化「影棚」环境贴图（等距柱状投影）
 *
 * 金属之所以像金属，是因为它反射的**光源是有形状的**。
 * 真实影棚用柔光箱，金属上就会出现一条条长条高光。
 * 这里画 4 块柔光箱 + 1 块暖色反光板 + 地平线冷光，
 * 拿去当 scene.environment，金属立刻就有质感。
 */
export function createStudioEnvTexture(width = 2048) {
  const w = width;
  const h = width / 2;
  const canvas = newCanvas(w, h);
  const ctx = canvas.getContext('2d');

  // --- 底色：上冷下暗 ---
  const bg = ctx.createLinearGradient(0, 0, 0, h);
  bg.addColorStop(0, '#0a1220');
  bg.addColorStop(0.42, '#111d30');
  bg.addColorStop(0.52, '#0d1728');
  bg.addColorStop(1, '#03060c');
  ctx.fillStyle = bg;
  ctx.fillRect(0, 0, w, h);

  /** 画一块柔光箱：中心亮、四周柔和衰减 */
  const softbox = (u, v, sizeU, sizeV, color, alpha) => {
    const cx = u * w;
    const cy = v * h;
    const rx = sizeU * w;
    const ry = sizeV * h;
    // 用多层椭圆叠加模拟方形柔光箱的柔边
    for (let i = 6; i >= 1; i--) {
      const k = i / 6;
      const g = ctx.createRadialGradient(cx, cy, 0, cx, cy, Math.max(rx, ry) * k);
      g.addColorStop(0, `rgba(${color},${alpha * (1 - k * 0.55)})`);
      g.addColorStop(1, `rgba(${color},0)`);
      ctx.fillStyle = g;
      ctx.beginPath();
      ctx.ellipse(cx, cy, rx * k, ry * k, 0, 0, Math.PI * 2);
      ctx.fill();
    }
  };

  // 主柔光箱（右上，冷白）—— 负责金属上的主高光
  softbox(0.14, 0.2, 0.13, 0.17, '255,253,248', 1.0);
  // 左侧长条柔光箱（青白）—— 拉出长条高光
  softbox(0.44, 0.3, 0.1, 0.1, '190,236,255', 0.95);
  // 右侧补光（科技蓝）
  softbox(0.72, 0.26, 0.12, 0.13, '110,185,255', 0.7);
  // 背面暖色反光板（红），对应基座的红色警戒光
  softbox(0.86, 0.36, 0.09, 0.08, '255,105,118', 0.5);
  // 顶部青蓝补光，给装甲板一层冷调
  softbox(0.3, 0.06, 0.22, 0.07, '175,225,255', 0.55);
  // 底部一圈青色地面反光（科技舱室的地光）
  softbox(0.55, 0.72, 0.5, 0.1, '70,170,255', 0.28);

  // 地平线冷光带
  const horizon = ctx.createLinearGradient(0, h * 0.46, 0, h * 0.6);
  horizon.addColorStop(0, 'rgba(90,190,255,0)');
  horizon.addColorStop(0.4, 'rgba(90,190,255,0.2)');
  horizon.addColorStop(1, 'rgba(40,110,190,0)');
  ctx.fillStyle = horizon;
  ctx.fillRect(0, h * 0.46, w, h * 0.14);

  const t = tex(canvas, { srgb: true, aniso: 4 });
  t.mapping = THREE.EquirectangularReflectionMapping;
  return t;
}

/**
 * 盾牌装甲 PBR 套图
 * @param {Array<{x:number,y:number}>} pts 盾形轮廓点（物体空间）
 * @param {object} opts
 */
export function createShieldPBR(pts, opts = {}) {
  const S = opts.size || 2048;
  const rand = makeRandom(opts.seed || 4711);
  const cx = opts.cx ?? 0;
  const cy = opts.cy ?? 0;
  const spanX = opts.spanX || 2.2;
  const spanY = opts.spanY || 2.9;

  // 轮廓 → 贴图坐标（统一用方形包围盒，保证形状比例正确）
  const span = Math.max(spanX, spanY);
  const mapPoint = (p) => [
    ((p.x - cx) / span + 0.5) * S,
    (0.5 - (p.y - cy) / span) * S,
  ];

  const path = (ctx) => {
    ctx.beginPath();
    pts.forEach((p, i) => {
      const [x, y] = mapPoint(p);
      if (i === 0) ctx.moveTo(x, y);
      else ctx.lineTo(p === pts[0] ? x : x, y);
    });
    ctx.closePath();
  };

  // ==========================================================
  // 科技装饰：所有贴图共用同一套坐标，保证高度/反照率/自发光完全对齐
  // ==========================================================
  const SEAMS = [
    [0.06, 0.30, 0.94, 0.30],
    [0.06, 0.68, 0.94, 0.68],
    [0.50, 0.06, 0.50, 0.30],
    [0.50, 0.68, 0.50, 0.90],
    [0.08, 0.17, 0.27, 0.07],
    [0.92, 0.17, 0.73, 0.07],
  ];

  // 电路走线：从分缝上分叉出来的直角折线
  const traces = [];
  for (let i = 0; i < 30; i++) {
    const seam = SEAMS[i % SEAMS.length];
    const t = 0.08 + rand() * 0.84;
    const x0 = seam[0] + (seam[2] - seam[0]) * t;
    const y0 = seam[1] + (seam[3] - seam[1]) * t;
    const horizontalSeam = Math.abs(seam[3] - seam[1]) < 1e-6;
    const dir = rand() > 0.5 ? 1 : -1;
    const len = 0.035 + rand() * 0.065;
    const turn = 0.025 + rand() * 0.055;
    const line = [[x0, y0]];
    if (horizontalSeam) {
      const y1 = y0 + dir * len;
      line.push([x0, y1]);
      line.push([x0 + dir * turn, y1]);
    } else {
      const x1 = x0 + dir * len;
      line.push([x1, y0]);
      line.push([x1, y0 + dir * turn]);
    }
    traces.push(line);
  }

  // 散热槽组
  const VENTS = [
    { x: 0.185, y: 0.775, n: 5, gap: 0.021, len: 0.072 },
    { x: 0.735, y: 0.775, n: 5, gap: 0.021, len: 0.072 },
    { x: 0.195, y: 0.205, n: 4, gap: 0.019, len: 0.058 },
  ];

  // 圆形接口
  const PORTS = [
    { x: 0.305, y: 0.415, r: 0.034 },
    { x: 0.715, y: 0.535, r: 0.027 },
  ];

  // 指示灯（走线末端的小节点）
  const DOTS = traces.map((t) => t[t.length - 1]);

  /** 把整套科技装饰画到指定通道上 */
  const drawTech = (ctx, mode) => {
    const isH = mode === 'height';
    const isA = mode === 'albedo';
    const isE = mode === 'emissive';

    // ---- 散热槽：凹槽，不发光 ----
    VENTS.forEach((v) => {
      for (let i = 0; i < v.n; i++) {
        const x = (v.x + i * v.gap) * S;
        const y = v.y * S;
        const w = v.len * S;
        ctx.save();
        if (isE) {
          ctx.fillStyle = 'rgba(40,150,210,0.10)';
        } else if (isH) {
          ctx.fillStyle = 'rgba(38,38,38,0.95)';
        } else {
          ctx.fillStyle = 'rgba(5,16,34,0.85)';
        }
        ctx.fillRect(x, y, w, S * 0.008);
        if (isH) {
          ctx.fillStyle = 'rgba(200,200,200,0.45)';
          ctx.fillRect(x, y + S * 0.008, w, S * 0.002);
        }
        ctx.restore();
      }
    });

    // ---- 圆形接口 ----
    PORTS.forEach((p) => {
      const x = p.x * S;
      const y = p.y * S;
      const r = p.r * S;
      ctx.save();
      if (isE) {
        ctx.strokeStyle = 'rgba(120,225,255,0.9)';
        ctx.lineWidth = S * 0.0035;
        ctx.beginPath();
        ctx.arc(x, y, r, 0, Math.PI * 2);
        ctx.stroke();
        ctx.fillStyle = 'rgba(70,190,240,0.18)';
        ctx.beginPath();
        ctx.arc(x, y, r * 0.72, 0, Math.PI * 2);
        ctx.fill();
      } else {
        ctx.strokeStyle = isH ? 'rgba(44,44,44,0.95)' : 'rgba(6,18,38,0.8)';
        ctx.lineWidth = S * 0.005;
        ctx.beginPath();
        ctx.arc(x, y, r, 0, Math.PI * 2);
        ctx.stroke();
        ctx.fillStyle = isH ? 'rgba(44,44,44,0.7)' : 'rgba(4,12,28,0.75)';
        ctx.beginPath();
        ctx.arc(x, y, r * 0.66, 0, Math.PI * 2);
        ctx.fill();
      }
      ctx.restore();
    });

    // ---- 电路走线 ----
    traces.forEach((line) => {
      const pts = line.map(([x, y]) => [x * S, y * S]);
      ctx.save();
      if (isE) {
        ctx.strokeStyle = 'rgba(70,190,255,0.22)';
        ctx.lineWidth = S * 0.010;
        ctx.filter = 'blur(7px)';
        ctx.beginPath();
        ctx.moveTo(pts[0][0], pts[0][1]);
        for (let i = 1; i < pts.length; i++) ctx.lineTo(pts[i][0], pts[i][1]);
        ctx.stroke();
        ctx.filter = 'none';
        ctx.strokeStyle = 'rgba(175,240,255,0.95)';
        ctx.lineWidth = S * 0.0022;
      } else {
        ctx.strokeStyle = isH ? 'rgba(52,52,52,0.9)' : 'rgba(7,22,44,0.8)';
        ctx.lineWidth = S * 0.0035;
      }
      ctx.beginPath();
      ctx.moveTo(pts[0][0], pts[0][1]);
      for (let i = 1; i < pts.length; i++) ctx.lineTo(pts[i][0], pts[i][1]);
      ctx.stroke();
      ctx.restore();
    });

    // ---- 指示灯节点 ----
    DOTS.forEach(([dx, dy]) => {
      const x = dx * S;
      const y = dy * S;
      ctx.save();
      if (isE) {
        ctx.fillStyle = 'rgba(190,245,255,0.95)';
        ctx.beginPath();
        ctx.arc(x, y, S * 0.005, 0, Math.PI * 2);
        ctx.fill();
        ctx.fillStyle = 'rgba(90,210,255,0.3)';
        ctx.filter = 'blur(6px)';
        ctx.beginPath();
        ctx.arc(x, y, S * 0.013, 0, Math.PI * 2);
        ctx.fill();
      } else {
        ctx.fillStyle = isH ? 'rgba(60,60,60,0.9)' : 'rgba(150,205,245,0.35)';
        ctx.beginPath();
        ctx.arc(x, y, S * 0.004, 0, Math.PI * 2);
        ctx.fill();
      }
      ctx.restore();
    });

    // ---- 面板分缝（最后画，压在走线之上）----
    SEAMS.forEach((sm) => {
      const [x1, y1, x2, y2] = sm.map((v) => v * S);
      ctx.save();
      if (isE) {
        ctx.strokeStyle = 'rgba(50,180,255,0.34)';
        ctx.lineWidth = S * 0.018;
        ctx.filter = 'blur(11px)';
        ctx.beginPath();
        ctx.moveTo(x1, y1);
        ctx.lineTo(x2, y2);
        ctx.stroke();
        ctx.filter = 'none';
        ctx.strokeStyle = 'rgba(165,240,255,0.98)';
        ctx.lineWidth = S * 0.0032;
        ctx.beginPath();
        ctx.moveTo(x1, y1);
        ctx.lineTo(x2, y2);
        ctx.stroke();
      } else {
        ctx.strokeStyle = isH ? 'rgba(40,40,40,0.95)' : 'rgba(6,22,46,0.85)';
        ctx.lineWidth = S * 0.005;
        ctx.beginPath();
        ctx.moveTo(x1, y1);
        ctx.lineTo(x2, y2);
        ctx.stroke();
        // 缝的一侧留一道高光边
        ctx.strokeStyle = isH ? 'rgba(205,205,205,0.55)' : 'rgba(120,195,255,0.22)';
        ctx.lineWidth = S * 0.0016;
        ctx.beginPath();
        ctx.moveTo(x1, y1 - S * 0.004);
        ctx.lineTo(x2, y2 - S * 0.004);
        ctx.stroke();
      }
      ctx.restore();
    });
  };

  // ==========================================================
  // 高度图
  // ==========================================================
  const hC = newCanvas(S, S);
  const h = hC.getContext('2d');
  h.fillStyle = '#808080';
  h.fillRect(0, 0, S, S);

  // 大尺度起伏（金属板轻微不平）
  for (let i = 0; i < 40; i++) {
    const x = rand() * S;
    const y = rand() * S;
    const r = S * (0.05 + rand() * 0.16);
    const g = h.createRadialGradient(x, y, 0, x, y, r);
    const v = 128 + (rand() - 0.5) * 22;
    g.addColorStop(0, `rgba(${v},${v},${v},0.5)`);
    g.addColorStop(1, 'rgba(128,128,128,0)');
    h.fillStyle = g;
    h.fillRect(x - r, y - r, r * 2, r * 2);
  }

  // 细密拉丝（阳极氧化的机器加工纹）
  h.save();
  path(h);
  h.clip();
  for (let i = 0; i < 6000; i++) {
    const y = rand() * S;
    const x = rand() * S;
    const len = 30 + rand() * 460;
    const v = 128 + (rand() - 0.5) * 26;
    h.strokeStyle = `rgba(${v},${v},${v},${0.08 + rand() * 0.16})`;
    h.lineWidth = 0.5 + rand() * 1.2;
    h.beginPath();
    h.moveTo(x, y);
    h.lineTo(x + len, y + (rand() - 0.5) * 2);
    h.stroke();
  }
  h.restore();

  // 科技装饰进高度图
  h.save();
  path(h);
  h.clip();
  drawTech(h, 'height');
  h.restore();

  // 边缘倒角：沿轮廓描一圈软边
  h.save();
  h.globalAlpha = 0.85;
  h.strokeStyle = 'rgba(215,215,215,0.95)';
  h.lineWidth = S * 0.016;
  h.filter = 'blur(6px)';
  path(h);
  h.stroke();
  h.restore();

  // ==========================================================
  // 反照率（科技蓝）
  // ==========================================================
  const aC = newCanvas(S, S);
  const a = aC.getContext('2d');
  a.fillStyle = '#0a1c33';
  a.fillRect(0, 0, S, S);

  a.save();
  path(a);
  a.clip();

  // 饱和的科技蓝金属
  const base = a.createLinearGradient(0, S * 0.02, 0, S * 0.98);
  base.addColorStop(0, '#5c9ae0');
  base.addColorStop(0.32, '#2f6ec0');
  base.addColorStop(0.62, '#21579f');
  base.addColorStop(0.86, '#194583');
  base.addColorStop(1, '#133366');
  a.fillStyle = base;
  a.fillRect(0, 0, S, S);

  // 冷暖斑（阳极氧化不均）
  for (let i = 0; i < 30; i++) {
    const x = rand() * S;
    const y = rand() * S;
    const r = S * (0.08 + rand() * 0.2);
    const g = a.createRadialGradient(x, y, 0, x, y, r);
    const cyan = rand() > 0.45;
    g.addColorStop(0, cyan ? 'rgba(90,200,255,0.16)' : 'rgba(30,70,160,0.2)');
    g.addColorStop(1, 'rgba(0,0,0,0)');
    a.fillStyle = g;
    a.fillRect(x - r, y - r, r * 2, r * 2);
  }

  // 拉丝明暗
  for (let i = 0; i < 2800; i++) {
    const y = rand() * S;
    const x = rand() * S;
    const len = 40 + rand() * 400;
    a.strokeStyle = rand() > 0.5 ? 'rgba(190,230,255,0.07)' : 'rgba(0,10,40,0.1)';
    a.lineWidth = 0.6 + rand() * 1.3;
    a.beginPath();
    a.moveTo(x, y);
    a.lineTo(x + len, y);
    a.stroke();
  }

  // 细划痕
  for (let i = 0; i < 90; i++) {
    const x = rand() * S;
    const y = rand() * S;
    const ang = (rand() - 0.5) * 0.6;
    const len = 12 + rand() * 70;
    a.strokeStyle = `rgba(215,240,255,${0.05 + rand() * 0.1})`;
    a.lineWidth = 0.6 + rand();
    a.beginPath();
    a.moveTo(x, y);
    a.lineTo(x + Math.cos(ang) * len, y + Math.sin(ang) * len);
    a.stroke();
  }
  a.restore();

  // 边缘磨损：轮廓一圈被磨亮的钢蓝
  a.save();
  a.globalCompositeOperation = 'source-atop';
  a.filter = 'blur(9px)';
  a.strokeStyle = 'rgba(170,215,255,0.6)';
  a.lineWidth = S * 0.012;
  path(a);
  a.stroke();
  a.restore();

  // 科技装饰进反照率
  a.save();
  path(a);
  a.clip();
  a.globalCompositeOperation = 'source-atop';
  drawTech(a, 'albedo');
  a.restore();

  // ==========================================================
  // 自发光：只有电路缝、走线、指示灯发青光
  // ==========================================================
  const eC = newCanvas(S, S);
  const e = eC.getContext('2d');
  e.fillStyle = '#000000';
  e.fillRect(0, 0, S, S);
  e.save();
  path(e);
  e.clip();
  drawTech(e, 'emissive');
  e.restore();

  // ==========================================================
  // 粗糙度 / 金属度
  // ==========================================================
  const rC = newCanvas(S, S);
  const r = rC.getContext('2d');
  r.fillStyle = '#4a4a4a';
  r.fillRect(0, 0, S, S);
  r.save();
  path(r);
  r.clip();
  for (let i = 0; i < 2400; i++) {
    const y = rand() * S;
    const x = rand() * S;
    const len = 40 + rand() * 360;
    const v = rand() > 0.5 ? 255 : 0;
    r.strokeStyle = `rgba(${v},${v},${v},${0.025 + rand() * 0.05})`;
    r.lineWidth = 1 + rand() * 2.2;
    r.beginPath();
    r.moveTo(x, y);
    r.lineTo(x + len, y);
    r.stroke();
  }
  r.restore();
  // 边缘更光滑
  r.save();
  r.globalCompositeOperation = 'source-atop';
  r.filter = 'blur(11px)';
  r.strokeStyle = 'rgba(44,44,44,0.7)';
  r.lineWidth = S * 0.014;
  path(r);
  r.stroke();
  r.restore();

  const mC = newCanvas(S, S);
  const m = mC.getContext('2d');
  m.fillStyle = '#f0f0f0';
  m.fillRect(0, 0, S, S);
  m.save();
  path(m);
  m.clip();
  for (let i = 0; i < 16; i++) {
    const x = rand() * S;
    const y = rand() * S;
    const rrad = S * (0.03 + rand() * 0.08);
    const g = m.createRadialGradient(x, y, 0, x, y, rrad);
    g.addColorStop(0, 'rgba(150,150,150,0.45)');
    g.addColorStop(1, 'rgba(150,150,150,0)');
    m.fillStyle = g;
    m.fillRect(x - rrad, y - rrad, rrad * 2, rrad * 2);
  }
  m.restore();

  // ==========================================================
  // 打包
  // ==========================================================
  const normalC = heightToNormal(hC, opts.bump ?? 2.6);
  const aoC = heightToAO(hC, 1.8);

  return {
    map: tex(aC, { srgb: true }),
    normalMap: tex(normalC),
    roughnessMap: tex(rC),
    metalnessMap: tex(mC),
    aoMap: tex(aoC),
    emissiveMap: tex(eC, { srgb: true }),
  };
}

/**
 * 基座台面 PBR：同心拉丝金属 + 真实环槽 + 扇形刻度
 */
export function createRadialDeckPBR(opts = {}) {
  const S = opts.size || 2048;
  const rand = makeRandom(opts.seed || 909);
  const c = S / 2;

  const hC = newCanvas(S, S);
  const h = hC.getContext('2d');
  h.fillStyle = '#808080';
  h.fillRect(0, 0, S, S);
  h.translate(c, c);

  // 同心拉丝
  for (let i = 0; i < 4200; i++) {
    const rad = rand() * c;
    const a0 = rand() * Math.PI * 2;
    const sweep = 0.05 + rand() * 0.5;
    const v = 128 + (rand() - 0.5) * 46;
    h.strokeStyle = `rgba(${v},${v},${v},${0.12 + rand() * 0.2})`;
    h.lineWidth = 0.6 + rand() * 1.8;
    h.beginPath();
    h.arc(0, 0, rad, a0, a0 + sweep);
    h.stroke();
  }

  // 环槽（真的凹下去）
  const ringGrooves = [0.22, 0.3, 0.38, 0.46, 0.54, 0.64, 0.72, 0.8, 0.88];
  ringGrooves.forEach((rr, i) => {
    const rad = rr * c;
    h.strokeStyle = 'rgba(46,46,46,0.95)';
    h.lineWidth = S * (i % 3 === 0 ? 0.008 : 0.004);
    h.beginPath();
    h.arc(0, 0, rad, 0, Math.PI * 2);
    h.stroke();
    h.strokeStyle = 'rgba(200,200,200,0.5)';
    h.lineWidth = S * 0.002;
    h.beginPath();
    h.arc(0, 0, rad + S * 0.005, 0, Math.PI * 2);
    h.stroke();
  });

  // 扇形刻度
  for (let i = 0; i < 72; i++) {
    const ang = (i / 72) * Math.PI * 2;
    const long = i % 6 === 0;
    const r1 = 0.9 * c;
    const r2 = (long ? 0.965 : 0.94) * c;
    h.strokeStyle = 'rgba(52,52,52,0.9)';
    h.lineWidth = S * (long ? 0.006 : 0.003);
    h.beginPath();
    h.moveTo(Math.cos(ang) * r1, Math.sin(ang) * r1);
    h.lineTo(Math.cos(ang) * r2, Math.sin(ang) * r2);
    h.stroke();
  }
  h.setTransform(1, 0, 0, 1, 0, 0);

  // ---------------- 反照率 ----------------
  const aC = newCanvas(S, S);
  const a = aC.getContext('2d');
  a.fillStyle = '#241c26';
  a.fillRect(0, 0, S, S);
  a.translate(c, c);

  const base = a.createRadialGradient(0, 0, 0, 0, 0, c);
  base.addColorStop(0, '#4a3844');
  base.addColorStop(0.55, '#332632');
  base.addColorStop(1, '#241b27');
  a.fillStyle = base;
  a.beginPath();
  a.arc(0, 0, c, 0, Math.PI * 2);
  a.fill();

  // 同心拉丝
  for (let i = 0; i < 2600; i++) {
    const rad = rand() * c;
    const a0 = rand() * Math.PI * 2;
    const sweep = 0.05 + rand() * 0.6;
    a.strokeStyle = rand() > 0.5 ? 'rgba(225,205,220,0.1)' : 'rgba(0,0,0,0.12)';
    a.lineWidth = 0.7 + rand() * 1.6;
    a.beginPath();
    a.arc(0, 0, rad, a0, a0 + sweep);
    a.stroke();
  }
  // 环槽压暗
  ringGrooves.forEach((rr) => {
    a.strokeStyle = 'rgba(0,0,0,0.8)';
    a.lineWidth = S * 0.005;
    a.beginPath();
    a.arc(0, 0, rr * c, 0, Math.PI * 2);
    a.stroke();
  });
  // 中心徽记区
  a.fillStyle = 'rgba(8,6,12,0.92)';
  a.beginPath();
  a.arc(0, 0, 0.19 * c, 0, Math.PI * 2);
  a.fill();
  a.setTransform(1, 0, 0, 1, 0, 0);

  // ---------------- 粗糙度 ----------------
  const rC = newCanvas(S, S);
  const r = rC.getContext('2d');
  r.fillStyle = '#808080';
  r.fillRect(0, 0, S, S);
  r.translate(c, c);
  for (let i = 0; i < 2000; i++) {
    const rad = rand() * c;
    const a0 = rand() * Math.PI * 2;
    const sweep = 0.05 + rand() * 0.6;
    const v = rand() > 0.5 ? 255 : 0;
    r.strokeStyle = `rgba(${v},${v},${v},${0.03 + rand() * 0.05})`;
    r.lineWidth = 1 + rand() * 2;
    r.beginPath();
    r.arc(0, 0, rad, a0, a0 + sweep);
    r.stroke();
  }
  r.setTransform(1, 0, 0, 1, 0, 0);

  // ---------------- 金属度 ----------------
  const mC = newCanvas(S, S);
  const m = mC.getContext('2d');
  m.fillStyle = '#e8e8e8';
  m.fillRect(0, 0, S, S);

  // ---------------- 自发光：只有环槽与刻度发红光 ----------------
  const eC = newCanvas(S, S);
  const e = eC.getContext('2d');
  e.fillStyle = '#000000';
  e.fillRect(0, 0, S, S);
  e.translate(c, c);
  ringGrooves.forEach((rr, i) => {
    const g = e.createRadialGradient(0, 0, (rr - 0.012) * c, 0, 0, (rr + 0.012) * c);
    g.addColorStop(0, 'rgba(0,0,0,0)');
    g.addColorStop(0.5, `rgba(255,70,96,${i % 3 === 0 ? 0.95 : 0.55})`);
    g.addColorStop(1, 'rgba(0,0,0,0)');
    e.fillStyle = g;
    e.beginPath();
    e.arc(0, 0, (rr + 0.012) * c, 0, Math.PI * 2);
    e.fill();
  });
  // 扇形刻度也微微发光
  for (let i = 0; i < 72; i++) {
    const ang = (i / 72) * Math.PI * 2;
    const long = i % 6 === 0;
    const r1 = 0.9 * c;
    const r2 = (long ? 0.965 : 0.94) * c;
    e.strokeStyle = long ? 'rgba(255,90,110,0.8)' : 'rgba(255,90,110,0.35)';
    e.lineWidth = S * (long ? 0.006 : 0.003);
    e.beginPath();
    e.moveTo(Math.cos(ang) * r1, Math.sin(ang) * r1);
    e.lineTo(Math.cos(ang) * r2, Math.sin(ang) * r2);
    e.stroke();
  }
  e.setTransform(1, 0, 0, 1, 0, 0);

  const normalC = heightToNormal(hC, opts.bump ?? 2.0);

  return {
    map: tex(aC, { srgb: true }),
    normalMap: tex(normalC),
    roughnessMap: tex(rC),
    metalnessMap: tex(mC),
    aoMap: tex(heightToAO(hC, 2.0)),
    emissiveMap: tex(eC, { srgb: true }),
  };
}

/**
 * 基座侧壁：竖向拉丝金属 + 散热槽
 */
export function createSidePanelPBR(opts = {}) {
  const S = opts.size || 1024;
  const rand = makeRandom(opts.seed || 3311);

  const hC = newCanvas(S, S);
  const h = hC.getContext('2d');
  h.fillStyle = '#808080';
  h.fillRect(0, 0, S, S);

  // 竖向拉丝
  for (let i = 0; i < 2200; i++) {
    const x = rand() * S;
    const y = rand() * S;
    const len = 20 + rand() * 260;
    const v = 128 + (rand() - 0.5) * 40;
    h.strokeStyle = `rgba(${v},${v},${v},${0.1 + rand() * 0.2})`;
    h.lineWidth = 0.6 + rand() * 1.6;
    h.beginPath();
    h.moveTo(x, y);
    h.lineTo(x, y + len);
    h.stroke();
  }

  // 散热槽（横向凹槽，分三组）
  const slotRows = [0.3, 0.5, 0.7];
  slotRows.forEach((ry) => {
    for (let g = 0; g < 3; g++) {
      const x0 = S * (0.12 + g * 0.26);
      const x1 = x0 + S * 0.18;
      const y = S * ry;
      h.strokeStyle = 'rgba(40,40,40,0.95)';
      h.lineWidth = S * 0.028;
      h.beginPath();
      h.moveTo(x0, y);
      h.lineTo(x1, y);
      h.stroke();
      h.strokeStyle = 'rgba(205,205,205,0.55)';
      h.lineWidth = S * 0.006;
      h.beginPath();
      h.moveTo(x0, y - S * 0.016);
      h.lineTo(x1, y - S * 0.016);
      h.stroke();
    }
  });

  const aC = newCanvas(S, S);
  const a = aC.getContext('2d');
  a.fillStyle = '#272030';
  a.fillRect(0, 0, S, S);
  for (let i = 0; i < 1600; i++) {
    const x = rand() * S;
    const y = rand() * S;
    const len = 20 + rand() * 220;
    a.strokeStyle = rand() > 0.5 ? 'rgba(210,200,215,0.1)' : 'rgba(0,0,0,0.12)';
    a.lineWidth = 0.7 + rand() * 1.4;
    a.beginPath();
    a.moveTo(x, y);
    a.lineTo(x, y + len);
    a.stroke();
  }
  // 槽口压暗
  slotRows.forEach((ry) => {
    for (let g = 0; g < 3; g++) {
      const x0 = S * (0.12 + g * 0.26);
      const x1 = x0 + S * 0.18;
      a.strokeStyle = 'rgba(0,0,0,0.85)';
      a.lineWidth = S * 0.026;
      a.beginPath();
      a.moveTo(x0, S * ry);
      a.lineTo(x1, S * ry);
      a.stroke();
    }
  });

  const rC = newCanvas(S, S);
  const r = rC.getContext('2d');
  r.fillStyle = '#8c8c8c';
  r.fillRect(0, 0, S, S);

  const mC = newCanvas(S, S);
  const m = mC.getContext('2d');
  m.fillStyle = '#eeeeee';
  m.fillRect(0, 0, S, S);

  return {
    map: tex(aC, { srgb: true }),
    normalMap: tex(heightToNormal(hC, 2.4)),
    roughnessMap: tex(rC),
    metalnessMap: tex(mC),
    aoMap: tex(heightToAO(hC, 1.6)),
  };
}
