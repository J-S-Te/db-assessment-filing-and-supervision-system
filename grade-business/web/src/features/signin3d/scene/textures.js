import * as THREE from 'three-signin';

/**
 * 贴图工厂：全部用 Canvas 程序化生成，不依赖外部图片资源，
 * 保证在任何环境下都能跑起来（除背景星空外）。
 */

/** 中文字体栈：各系统按顺序回退 */
export const FONT_STACK =
  '"PingFang SC","Hiragino Sans GB","Microsoft YaHei","Source Han Sans SC","Noto Sans CJK SC","WenQuanYi Micro Hei",sans-serif';

/** 简易可复现随机数（线性同余），保证每次生成的纹理一致 */
function makeRandom(seed = 20260821) {
  let s = seed >>> 0;
  return () => {
    s = (s * 1664525 + 1013904223) >>> 0;
    return s / 4294967296;
  };
}

function newCanvas(w, h) {
  const canvas = document.createElement('canvas');
  canvas.width = w;
  canvas.height = h;
  return canvas;
}

function finishTexture(canvas, { srgb = true, repeat = null, aniso = 8 } = {}) {
  const tex = new THREE.CanvasTexture(canvas);
  if (srgb) tex.colorSpace = THREE.SRGBColorSpace;
  tex.anisotropy = aniso;
  if (repeat) {
    tex.wrapS = THREE.RepeatWrapping;
    tex.wrapT = THREE.RepeatWrapping;
    tex.repeat.set(repeat[0], repeat[1]);
  }
  tex.needsUpdate = true;
  return tex;
}

/**
 * 「防」字核心徽记：圆形底盘 + 双环 + 发光汉字
 * 用作盾牌中央的核心面板
 */
export function createCoreEmblemTexture(char = '防') {
  const s = 512;
  const canvas = document.createElement('canvas');
  canvas.width = canvas.height = s;
  const ctx = canvas.getContext('2d');
  const cx = s / 2;
  const cy = s / 2;

  ctx.clearRect(0, 0, s, s);

  // 金属底盘：深色 + 同心拉丝
  const plate = ctx.createRadialGradient(cx, cy * 0.82, s * 0.05, cx, cy, s * 0.46);
  plate.addColorStop(0, 'rgba(30,52,82,0.95)');
  plate.addColorStop(0.55, 'rgba(14,28,50,0.92)');
  plate.addColorStop(1, 'rgba(6,12,24,0)');
  ctx.fillStyle = plate;
  ctx.beginPath();
  ctx.arc(cx, cy, s * 0.44, 0, Math.PI * 2);
  ctx.fill();

  ctx.save();
  ctx.beginPath();
  ctx.arc(cx, cy, s * 0.42, 0, Math.PI * 2);
  ctx.clip();
  for (let i = 0; i < 900; i++) {
    const r = (i / 900) * s * 0.42 + Math.random() * 2;
    const a0 = Math.random() * Math.PI * 2;
    const sweep = 0.1 + Math.random() * 0.8;
    ctx.strokeStyle = Math.random() > 0.5 ? 'rgba(150,190,230,0.05)' : 'rgba(0,0,0,0.07)';
    ctx.lineWidth = 0.6 + Math.random() * 1.4;
    ctx.beginPath();
    ctx.arc(cx, cy, r, a0, a0 + sweep);
    ctx.stroke();
  }
  ctx.restore();

  // 主环
  ctx.strokeStyle = 'rgba(150,225,255,0.75)';
  ctx.lineWidth = 4;
  ctx.beginPath();
  ctx.arc(cx, cy, s * 0.395, 0, Math.PI * 2);
  ctx.stroke();

  // 警戒副环（缺口设计）
  ctx.strokeStyle = 'rgba(255,110,132,0.55)';
  ctx.lineWidth = 2.5;
  for (let i = 0; i < 4; i++) {
    const a0 = (i / 4) * Math.PI * 2 + 0.25;
    ctx.beginPath();
    ctx.arc(cx, cy, s * 0.325, a0, a0 + 0.9);
    ctx.stroke();
  }

  // 刻度环
  ctx.strokeStyle = 'rgba(120,195,240,0.3)';
  ctx.lineWidth = 1.6;
  for (let i = 0; i < 48; i++) {
    const a = (i / 48) * Math.PI * 2;
    const r1 = s * 0.425;
    const r2 = i % 4 === 0 ? s * 0.455 : s * 0.442;
    ctx.beginPath();
    ctx.moveTo(cx + Math.cos(a) * r1, cy + Math.sin(a) * r1);
    ctx.lineTo(cx + Math.cos(a) * r2, cy + Math.sin(a) * r2);
    ctx.stroke();
  }

  // 「防」字：先画暗色蚀刻，再在下缘补一道高光，做出「刻进去」的立体感
  ctx.font = `700 ${Math.round(s * 0.34)}px ${FONT_STACK}`;
  ctx.textAlign = 'center';
  ctx.textBaseline = 'middle';

  ctx.fillStyle = 'rgba(0,0,0,0.9)';
  ctx.fillText(char, cx, cy + s * 0.012 + 3);

  ctx.shadowColor = 'rgba(120,225,255,0.9)';
  ctx.shadowBlur = 12;
  ctx.fillStyle = '#a9e2ff';
  ctx.fillText(char, cx, cy + s * 0.012);
  ctx.shadowBlur = 0;

  // 字体下缘的高光边（蚀刻的金属反光）
  ctx.save();
  ctx.globalCompositeOperation = 'source-atop';
  const bevel = ctx.createLinearGradient(0, cy - s * 0.16, 0, cy + s * 0.18);
  bevel.addColorStop(0, 'rgba(255,255,255,0)');
  bevel.addColorStop(0.62, 'rgba(255,255,255,0)');
  bevel.addColorStop(1, 'rgba(220,245,255,0.45)');
  ctx.fillStyle = bevel;
  ctx.fillRect(0, 0, s, s);
  ctx.restore();

  return finishTexture(canvas);
}

/**
 * 盾体表面电路纹理：深蓝金属底 + 曼哈顿走线 + 焊点
 * 作为盾体正面/背面的贴图
 */
export function createCircuitTexture() {
  const s = 1024;
  const canvas = newCanvas(s, s);
  const ctx = canvas.getContext('2d');
  const rand = makeRandom();

  // 底色
  const bg = ctx.createLinearGradient(0, 0, s, s);
  bg.addColorStop(0, '#16304f');
  bg.addColorStop(0.45, '#0e2038');
  bg.addColorStop(1, '#1a375e');
  ctx.fillStyle = bg;
  ctx.fillRect(0, 0, s, s);

  // 细网格
  ctx.strokeStyle = 'rgba(130,195,245,0.12)';
  ctx.lineWidth = 1;
  for (let i = 0; i <= s; i += 32) {
    ctx.beginPath();
    ctx.moveTo(i, 0);
    ctx.lineTo(i, s);
    ctx.stroke();
    ctx.beginPath();
    ctx.moveTo(0, i);
    ctx.lineTo(s, i);
    ctx.stroke();
  }

  // 曼哈顿走线
  ctx.lineCap = 'round';
  ctx.lineJoin = 'round';
  for (let i = 0; i < 46; i++) {
    const cyan = rand() > 0.28;
    const x = Math.round(rand() * s);
    const y = Math.round(rand() * s);
    const segs = 3 + Math.floor(rand() * 4);
    const len = 60 + rand() * 220;

    ctx.strokeStyle = cyan
      ? `rgba(130,230,255,${0.4 + rand() * 0.45})`
      : `rgba(255,130,150,${0.28 + rand() * 0.3})`;
    ctx.lineWidth = cyan ? 1.2 + rand() * 1.6 : 1 + rand();

    ctx.beginPath();
    ctx.moveTo(x, y);
    let cx = x;
    let cy = y;
    let dirX = rand() > 0.5 ? 1 : -1;
    for (let k = 0; k < segs; k++) {
      if (rand() > 0.45) {
        cx += dirX * len * (0.4 + rand() * 0.6);
        ctx.lineTo(cx, cy);
      } else {
        dirX = rand() > 0.5 ? 1 : -1;
        cy += (rand() > 0.5 ? 1 : -1) * len * (0.35 + rand() * 0.5);
        ctx.lineTo(cx, cy);
      }
    }
    ctx.stroke();

    // 端点焊点
    ctx.fillStyle = cyan ? 'rgba(150,235,255,0.75)' : 'rgba(255,140,155,0.6)';
    ctx.beginPath();
    ctx.arc(cx, cy, 2.6 + rand() * 2.4, 0, Math.PI * 2);
    ctx.fill();
  }

  // 大面积暗角，突出中央
  const vig = ctx.createRadialGradient(s / 2, s * 0.44, s * 0.08, s / 2, s / 2, s * 0.72);
  vig.addColorStop(0, 'rgba(255,255,255,0.1)');
  vig.addColorStop(0.5, 'rgba(0,0,0,0.05)');
  vig.addColorStop(1, 'rgba(0,0,0,0.42)');
  ctx.fillStyle = vig;
  ctx.fillRect(0, 0, s, s);

  return finishTexture(canvas, { aniso: 16 });
}

/** 粒子圆点精灵（地面点阵 / 星点） */
export function createDotSprite(inner = 'rgba(255,255,255,1)', outer = 'rgba(255,255,255,0)') {
  const s = 64;
  const canvas = newCanvas(s, s);
  const ctx = canvas.getContext('2d');
  const g = ctx.createRadialGradient(s / 2, s / 2, 0, s / 2, s / 2, s / 2);
  g.addColorStop(0, inner);
  g.addColorStop(0.35, inner);
  g.addColorStop(1, outer);
  ctx.fillStyle = g;
  ctx.fillRect(0, 0, s, s);
  return finishTexture(canvas, { srgb: true, aniso: 1 });
}

/** 能量光带条纹贴图：沿长度方向的流光 + 垂直方向渐隐 */
export function createEnergyStripTexture() {
  const w = 512;
  const h = 64;
  const canvas = newCanvas(w, h);
  const ctx = canvas.getContext('2d');
  const rand = makeRandom(77);

  ctx.clearRect(0, 0, w, h);

  // 垂直方向的柔化（中间亮）
  const vertical = ctx.createLinearGradient(0, 0, 0, h);
  vertical.addColorStop(0, 'rgba(255,255,255,0)');
  vertical.addColorStop(0.5, 'rgba(255,255,255,1)');
  vertical.addColorStop(1, 'rgba(255,255,255,0)');

  // 沿长度方向的流光条
  for (let i = 0; i < 16; i++) {
    const x = rand() * w;
    const len = 18 + rand() * 90;
    const alpha = 0.12 + rand() * 0.48;
    const g = ctx.createLinearGradient(x, 0, x + len, 0);
    g.addColorStop(0, 'rgba(255,255,255,0)');
    g.addColorStop(0.5, `rgba(255,255,255,${alpha})`);
    g.addColorStop(1, 'rgba(255,255,255,0)');
    ctx.fillStyle = g;
    ctx.fillRect(x, 0, len, h);
  }

  // 用垂直渐变做整体遮罩
  ctx.globalCompositeOperation = 'destination-in';
  ctx.fillStyle = vertical;
  ctx.fillRect(0, 0, w, h);
  ctx.globalCompositeOperation = 'source-over';

  return finishTexture(canvas, { repeat: [1, 1], aniso: 4 });
}

/** 基座顶面纹理：同心扫描环 + 扇形刻度 */
export function createBaseTopTexture() {
  const s = 1024;
  const canvas = newCanvas(s, s);
  const ctx = canvas.getContext('2d');
  const c = s / 2;

  ctx.fillStyle = '#070f1e';
  ctx.fillRect(0, 0, s, s);

  const glow = ctx.createRadialGradient(c, c, s * 0.04, c, c, s * 0.5);
  glow.addColorStop(0, 'rgba(255,70,95,0.42)');
  glow.addColorStop(0.55, 'rgba(180,25,55,0.14)');
  glow.addColorStop(1, 'rgba(6,12,26,0)');
  ctx.fillStyle = glow;
  ctx.fillRect(0, 0, s, s);

  // 同心扫描环
  ctx.strokeStyle = 'rgba(255,120,145,0.5)';
  for (let i = 1; i <= 9; i++) {
    ctx.lineWidth = i % 3 === 0 ? 2.4 : 1.1;
    ctx.beginPath();
    ctx.arc(c, c, (s * 0.47 * i) / 9, 0, Math.PI * 2);
    ctx.stroke();
  }

  // 放射刻度
  ctx.strokeStyle = 'rgba(255,150,170,0.38)';
  ctx.lineWidth = 1.6;
  for (let i = 0; i < 48; i++) {
    const a = (i / 48) * Math.PI * 2;
    const r1 = s * 0.40;
    const r2 = i % 4 === 0 ? s * 0.455 : s * 0.435;
    ctx.beginPath();
    ctx.moveTo(c + Math.cos(a) * r1, c + Math.sin(a) * r1);
    ctx.lineTo(c + Math.cos(a) * r2, c + Math.sin(a) * r2);
    ctx.stroke();
  }

  // 中心徽记区
  ctx.fillStyle = 'rgba(5,11,24,0.9)';
  ctx.beginPath();
  ctx.arc(c, c, s * 0.135, 0, Math.PI * 2);
  ctx.fill();
  ctx.strokeStyle = 'rgba(255,110,135,0.6)';
  ctx.lineWidth = 2.6;
  ctx.beginPath();
  ctx.arc(c, c, s * 0.135, 0, Math.PI * 2);
  ctx.stroke();

  return finishTexture(canvas, { aniso: 8 });
}

/** 能量竖直光柱贴图：底部亮、上部淡出 */
export function createBeamTexture() {
  const w = 128;
  const h = 512;
  const canvas = newCanvas(w, h);
  const ctx = canvas.getContext('2d');
  const g = ctx.createLinearGradient(0, h, 0, 0);
  g.addColorStop(0, 'rgba(120,220,255,0.5)');
  g.addColorStop(0.35, 'rgba(90,190,255,0.22)');
  g.addColorStop(1, 'rgba(60,140,255,0)');
  ctx.fillStyle = g;
  ctx.fillRect(0, 0, w, h);

  // 横向柔化
  const side = ctx.createLinearGradient(0, 0, w, 0);
  side.addColorStop(0, 'rgba(0,0,0,1)');
  side.addColorStop(0.5, 'rgba(0,0,0,0)');
  side.addColorStop(1, 'rgba(0,0,0,1)');
  ctx.globalCompositeOperation = 'destination-out';
  ctx.fillStyle = side;
  ctx.fillRect(0, 0, w, h);
  ctx.globalCompositeOperation = 'source-over';

  return finishTexture(canvas);
}
