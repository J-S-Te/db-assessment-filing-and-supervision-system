import * as THREE from 'three-signin';

/**
 * FUI（Future User Interface）未来操作系统界面
 *
 * 舱室里有三块空区，用全息仪表填满：
 *   A 左侧  SYSTEM TELEMETRY  系统遥测柱（纵向条形计量）
 *   B 顶部  SYSTEM STATUS      系统状态条（指示灯阵 + 动态波形）
 *   C 右侧  THREAT RADAR       态势感知雷达（扫描扇形 + 光点）
 *
 * 全部是场景里的真几何体（线段 / 细条 / 扇面 / 光点），
 * 用加色混合 + 辉光后期做出「悬浮全息」的质感；
 * 中文读数则交给 HTML 叠层投影定位，保证清晰。
 */

/** 全息线条材质：加色混合，半透明 */
function holoLine(color = 0x6fe4ff, opacity = 0.75) {
  return new THREE.LineBasicMaterial({
    color,
    transparent: true,
    opacity,
    blending: THREE.AdditiveBlending,
    depthWrite: false,
  });
}

/** 全息发光块：加色混合 */
function holoBlock(color = 0x6fe4ff, opacity = 0.9) {
  return new THREE.MeshBasicMaterial({
    color,
    transparent: true,
    opacity,
    blending: THREE.AdditiveBlending,
    depthWrite: false,
    side: THREE.DoubleSide, // 转到背面时全息图形不消失
  });
}

/** 深色凹槽材质（不发光，只用来「压」出层次） */
function slotMaterial() {
  return new THREE.MeshBasicMaterial({
    color: 0x061626,
    transparent: true,
    opacity: 0.55,
    depthWrite: false,
  });
}


/** 透明拾取面：让面板整块区域都能悬停/点击，但优先级低于实体物体 */
function pickProxy(w, h, z = 0) {
  const mesh = new THREE.Mesh(
    new THREE.PlaneGeometry(w, h),
    new THREE.MeshBasicMaterial({ visible: false })
  );
  mesh.userData.soft = true;
  mesh.position.z = z;
  return mesh;
}

/** 由若干线段组成一个折线框 */
function segGeometry(segs) {
  const arr = [];
  segs.forEach(([x1, y1, x2, y2]) => arr.push(x1, y1, 0, x2, y2, 0));
  const g = new THREE.BufferGeometry();
  g.setAttribute('position', new THREE.Float32BufferAttribute(arr, 3));
  return g;
}

/** 四角角标（FUI 面板的标志性元素） */
function cornerBrackets(w, h, len, color = 0x7fe8ff, opacity = 0.9) {
  const hw = w / 2;
  const hh = h / 2;
  const segs = [
    [-hw, -hh, -hw + len, -hh],
    [-hw, -hh, -hw, -hh + len],
    [hw, -hh, hw - len, -hh],
    [hw, -hh, hw, -hh + len],
    [-hw, hh, -hw + len, hh],
    [-hw, hh, -hw, hh - len],
    [hw, hh, hw - len, hh],
    [hw, hh, hw, hh - len],
  ];
  const lines = new THREE.LineSegments(segGeometry(segs), holoLine(color, opacity));
  return lines;
}

/** 雷达扫描扇形的径向渐变贴图 */
function createSweepTexture() {
  const size = 256;
  const canvas = document.createElement('canvas');
  canvas.width = canvas.height = size;
  const ctx = canvas.getContext('2d');
  const g = ctx.createRadialGradient(size / 2, size / 2, 0, size / 2, size / 2, size / 2);
  g.addColorStop(0, 'rgba(180,245,255,0.95)');
  g.addColorStop(0.35, 'rgba(90,210,255,0.42)');
  g.addColorStop(0.85, 'rgba(50,150,230,0.12)');
  g.addColorStop(1, 'rgba(40,120,200,0)');
  ctx.fillStyle = g;
  ctx.fillRect(0, 0, size, size);
  const tex = new THREE.CanvasTexture(canvas);
  tex.colorSpace = THREE.SRGBColorSpace;
  return tex;
}

/** 小光点精灵 */
function createBlipSprite() {
  const size = 64;
  const canvas = document.createElement('canvas');
  canvas.width = canvas.height = size;
  const ctx = canvas.getContext('2d');
  const g = ctx.createRadialGradient(size / 2, size / 2, 0, size / 2, size / 2, size / 2);
  g.addColorStop(0, 'rgba(255,255,255,1)');
  g.addColorStop(0.22, 'rgba(160,240,255,0.9)');
  g.addColorStop(1, 'rgba(60,170,255,0)');
  ctx.fillStyle = g;
  ctx.fillRect(0, 0, size, size);
  const tex = new THREE.CanvasTexture(canvas);
  tex.colorSpace = THREE.SRGBColorSpace;
  return tex;
}

/* ==========================================================
 * A. 左侧 · 系统遥测柱
 * ========================================================== */
function buildTelemetry(anchors) {
  const g = new THREE.Group();
  g.name = 'fuiTelemetry';

  const W = 1.82;
  const H = 4.3;

  // 外框角标
  g.add(cornerBrackets(W, H, 0.28, 0x7fe8ff, 0.85));

  // 透明拾取面（整块面板可悬停）
  const proxy = pickProxy(W, H);
  g.add(proxy);

  // 左侧主轴 + 刻度
  const axisX = -W / 2 + 0.22;
  const axisSegs = [[axisX, -H / 2 + 0.25, axisX, H / 2 - 0.25]];
  for (let i = 0; i <= 24; i++) {
    const y = -H / 2 + 0.25 + (i / 24) * (H - 0.5);
    const len = i % 4 === 0 ? 0.16 : 0.075;
    axisSegs.push([axisX, y, axisX + len, y]);
  }
  const axis = new THREE.LineSegments(segGeometry(axisSegs), holoLine(0x4fb8e8, 0.6));
  g.add(axis);

  // 条形计量
  const ROWS = [
    { label: '等级测评项目', value: '128 在办', ratio: 0.78, color: 0x5ce6ff },
    { label: '监督检查任务', value: '36 待复核', ratio: 0.52, color: 0x7fe8ff },
    { label: '备案审核件', value: '1 204 件', ratio: 0.68, color: 0x5ce6ff },
    { label: '整改闭环率', value: '92.4%', ratio: 0.924, color: 0x5ce6ff },
    { label: '高风险对象', value: '17 个', ratio: 0.24, color: 0xff7a90 },
    { label: '接入监管单位', value: '486 家', ratio: 0.82, color: 0x7fe8ff },
    { label: '在册测评机构', value: '63 家', ratio: 0.47, color: 0xffc46a },
  ];

  const barW = 0.86;
  const barH = 0.055;
  const top = H / 2 - 0.92;
  const step = (H - 1.85) / (ROWS.length - 1);

  ROWS.forEach((row, i) => {
    const y = top - i * step;

    // 凹槽
    const slot = new THREE.Mesh(new THREE.PlaneGeometry(barW, barH), slotMaterial());
    slot.position.set(axisX + 0.22 + barW / 2, y, 0);
    g.add(slot);

    // 发光填充
    const fillGeo = new THREE.PlaneGeometry(1, barH);
    const fill = new THREE.Mesh(fillGeo, holoBlock(row.color, 0.95));
    fill.position.set(axisX + 0.22, y, 0.002);
    g.add(fill);

    // 末端游标：普通混合，不参与加色辉光，否则会被 Bloom 糊成一块白斑
    const cursor = new THREE.Mesh(
      new THREE.PlaneGeometry(0.03, 0.1),
      new THREE.MeshBasicMaterial({ color: row.color, transparent: true, opacity: 0.9 })
    );
    g.add(cursor);

    // 文字锚点：数值靠右，名称在条形上方
    anchors.push({
      id: `tele-label-${i}`,
      panel: 'telemetry',
      kind: 'label',
      align: 'left',
      text: row.label,
      local: new THREE.Vector3(axisX + 0.24, y + 0.13, 0),
    });
    anchors.push({
      id: `tele-value-${i}`,
      panel: 'telemetry',
      kind: 'value',
      align: 'right',
      text: row.value,
      local: new THREE.Vector3(W / 2, y - 0.005, 0),
    });

    row._fill = fill;
    row._cursor = cursor;
    row._ratio = row.ratio;
  });

  // 顶部旋转六边形
  const hex = new THREE.LineLoop(
    new THREE.BufferGeometry().setFromPoints(
      Array.from({ length: 6 }, (_, i) => {
        const a = (i / 6) * Math.PI * 2;
        return new THREE.Vector3(Math.cos(a) * 0.19, Math.sin(a) * 0.19, 0);
      })
    ),
    holoLine(0x8fecff, 0.95)
  );
  hex.position.set(W / 2 - 0.2, -H / 2 + 0.22, 0);
  g.add(hex);
  const hexDot = new THREE.Mesh(
    new THREE.PlaneGeometry(0.06, 0.06),
    holoBlock(0xbff4ff, 1)
  );
  hexDot.position.copy(hex.position);
  g.add(hexDot);

  // 底部三颗指示灯
  const lampGeo = new THREE.PlaneGeometry(0.075, 0.075);
  for (let i = 0; i < 3; i++) {
    const lamp = new THREE.Mesh(lampGeo, holoBlock([0x5ce6ff, 0x7fe8ff, 0xffc46a][i], 0.95));
    lamp.position.set(axisX + 0.32 + i * 0.16, -H / 2 + 0.18, 0);
    g.add(lamp);
  }

  anchors.push({
    id: 'tele-title',
    panel: 'telemetry',
    kind: 'title',
    align: 'left',
    text: 'SUPERVISION TELEMETRY',
    local: new THREE.Vector3(-W / 2 + 0.26, H / 2 - 0.3, 0),
  });
  anchors.push({
    id: 'tele-sub',
    panel: 'telemetry',
    kind: 'sub',
    align: 'left',
    text: '监管运行态势',
    local: new THREE.Vector3(-W / 2 + 0.26, H / 2 - 0.3, 0),
    offsetY: 15,
  });

  return {
    group: g,
    update(t) {
      ROWS.forEach((row, i) => {
        // 数值轻微抖动，像实时采样
        const jitter = Math.sin(t * (1.1 + i * 0.37) + i) * 0.012;
        const len = Math.max(0.05, row._ratio * barW + jitter * barW);
        row._fill.scale.x = len;
        row._fill.position.x = axisX + 0.22 + len / 2;
        row._cursor.position.x = axisX + 0.22 + len;
      });
      hex.rotation.z = t * 0.35;
      hexDot.material.opacity = 0.7 + Math.sin(t * 2.6) * 0.3;
    },
  };
}

/* ==========================================================
 * B. 顶部 · 系统状态条
 * ========================================================== */
function buildStatusStrip(anchors) {
  const g = new THREE.Group();
  g.name = 'fuiStatus';

  const W = 5.35;
  const H = 0.9;

  g.add(cornerBrackets(W, H, 0.3, 0x7fe8ff, 0.85));

  // 透明拾取面
  const proxy = pickProxy(W, H);
  g.add(proxy);

  // 上下两条细线
  const frame = new THREE.LineSegments(
    segGeometry([
      [-W / 2 + 0.3, H / 2, W / 2 - 0.3, H / 2],
      [-W / 2 + 0.3, -H / 2, W / 2 - 0.3, -H / 2],
    ]),
    holoLine(0x4fb8e8, 0.5)
  );
  g.add(frame);

  // 指示灯阵（10 个小方块）
  const LAMPS = 10;
  const lampGeo = new THREE.PlaneGeometry(0.15, 0.15);
  const lampStart = -W / 2 + 2.3;
  const lamps = [];
  for (let i = 0; i < LAMPS; i++) {
    const on = i !== 6 && i !== 9; // 留两个表示告警
    const lamp = new THREE.Mesh(
      lampGeo,
      holoBlock(on ? 0x5ce6ff : 0xff5f78, on ? 0.95 : 0.55)
    );
    lamp.position.set(lampStart + i * 0.195, H / 2 - 0.27, 0);
    g.add(lamp);
    lamps.push({ lamp, on, phase: i * 0.4 });
  }

  // 动态波形线
  const wavePts = [];
  const waveSegs = [];
  const WAVE_N = 96;
  const waveX0 = -W / 2 + 0.5;
  const waveX1 = W / 2 - 0.5;
  for (let i = 0; i <= WAVE_N; i++) {
    const x = waveX0 + (i / WAVE_N) * (waveX1 - waveX0);
    wavePts.push(new THREE.Vector3(x, -H / 2 + 0.3, 0));
  }
  const waveGeo = new THREE.BufferGeometry().setFromPoints(wavePts);
  const wave = new THREE.Line(waveGeo, holoLine(0x7fe8ff, 0.85));
  g.add(wave);

  // 波形底部一条淡色基准线
  const base = new THREE.Line(
    new THREE.BufferGeometry().setFromPoints([
      new THREE.Vector3(waveX0, -H / 2 + 0.3, 0),
      new THREE.Vector3(waveX1, -H / 2 + 0.3, 0),
    ]),
    holoLine(0x2f7fb0, 0.35)
  );
  g.add(base);

  anchors.push({
    id: 'status-title',
    panel: 'status',
    kind: 'title',
    align: 'left',
    text: 'SUPERVISION GRID',
    local: new THREE.Vector3(-W / 2 + 0.45, H / 2 - 0.27, 0),
  });
  anchors.push({
    id: 'status-sub',
    panel: 'status',
    kind: 'sub',
    align: 'left',
    text: '监管网格接入正常',
    local: new THREE.Vector3(-W / 2 + 0.45, H / 2 - 0.27, 0),
    offsetY: 15,
  });
  anchors.push({
    id: 'status-right',
    panel: 'status',
    kind: 'value',
    align: 'right',
    text: '12 REGIONS',
    local: new THREE.Vector3(W / 2 - 0.45, H / 2 - 0.27, 0),
  });

  return {
    group: g,
    update(t) {
      lamps.forEach((l) => {
        l.lamp.material.opacity = l.on
          ? 0.65 + Math.sin(t * 2.2 + l.phase) * 0.3
          : 0.35 + Math.sin(t * 4.5 + l.phase) * 0.25;
      });
      // 波形：三个不同频率叠加，像实时信号
      const arr = waveGeo.attributes.position.array;
      for (let i = 0; i <= WAVE_N; i++) {
        const x = waveX0 + (i / WAVE_N) * (waveX1 - waveX0);
        const u = i / WAVE_N;
        const y =
          Math.sin(u * 22 + t * 3.1) * 0.055 +
          Math.sin(u * 41 - t * 2.2) * 0.028 +
          Math.sin(u * 9 + t * 1.4) * 0.04;
        arr[i * 3 + 1] = -H / 2 + 0.3 + y;
      }
      waveGeo.attributes.position.needsUpdate = true;
    },
  };
}

/* ==========================================================
 * C. 右侧 · 态势感知雷达
 * ========================================================== */
function buildRadar(anchors) {
  const g = new THREE.Group();
  g.name = 'fuiRadar';

  const R = 0.9;

  // 外框角标
  g.add(cornerBrackets(R * 2.05, R * 2.05, 0.26, 0x7fe8ff, 0.9));

  // 透明拾取面
  const proxy = pickProxy(R * 2.05, R * 2.05);
  g.add(proxy);

  // 同心圈
  const rings = [];
  [0.34, 0.56, 0.78, 1].forEach((k, i) => {
    const ring = new THREE.LineLoop(
      new THREE.BufferGeometry().setFromPoints(
        Array.from({ length: 96 }, (_, j) => {
          const a = (j / 96) * Math.PI * 2;
          return new THREE.Vector3(Math.cos(a) * R * k, Math.sin(a) * R * k, 0);
        })
      ),
      holoLine(i === 3 ? 0x8fecff : 0x3f9fd0, i === 3 ? 0.85 : 0.4)
    );
    g.add(ring);
    rings.push(ring);
  });

  // 十字线（虚线感：拆成短线段）
  const cross = [];
  for (let i = -7; i <= 7; i++) {
    if (i === 0) continue;
    const d = (i / 7) * R * 0.92;
    cross.push([d - 0.06, 0, d + 0.06, 0]);
    cross.push([0, d - 0.06, 0, d + 0.06]);
  }
  g.add(new THREE.LineSegments(segGeometry(cross), holoLine(0x4fb8e8, 0.45)));

  // 扫描扇形
  const sweep = new THREE.Mesh(
    new THREE.CircleGeometry(R, 48, 0, 0.72),
    new THREE.MeshBasicMaterial({
      map: createSweepTexture(),
      transparent: true,
      blending: THREE.AdditiveBlending,
      depthWrite: false,
      side: THREE.DoubleSide,
      opacity: 0.62,
    })
  );
  g.add(sweep);

  // 扫描前沿线
  const front = new THREE.Line(
    new THREE.BufferGeometry().setFromPoints([
      new THREE.Vector3(0, 0, 0.004),
      new THREE.Vector3(R, 0, 0.004),
    ]),
    holoLine(0xbdf6ff, 0.95)
  );
  g.add(front);

  // 中心点
  const center = new THREE.Mesh(
    new THREE.PlaneGeometry(0.1, 0.1),
    holoBlock(0xd8fbff, 1)
  );
  g.add(center);

  // 光点（目标回波）
  const sprite = createBlipSprite();
  const blips = [
    { a: 0.7, r: 0.62, hot: true },
    { a: 2.1, r: 0.4, hot: false },
    { a: 3.4, r: 0.78, hot: true },
    { a: 4.6, r: 0.3, hot: false },
    { a: 5.5, r: 0.55, hot: false },
  ];
  blips.forEach((b) => {
    const m = new THREE.Mesh(
      new THREE.PlaneGeometry(b.hot ? 0.22 : 0.14, b.hot ? 0.22 : 0.14),
      new THREE.MeshBasicMaterial({
        map: sprite,
        color: b.hot ? 0xff9db0 : 0x9beeff,
        transparent: true,
        blending: THREE.AdditiveBlending,
        depthWrite: false,
      })
    );
    m.position.set(Math.cos(b.a) * R * b.r, Math.sin(b.a) * R * b.r, 0.01);
    g.add(m);
    b.mesh = m;
  });

  // 右侧刻度尺
  const scaleSegs = [];
  for (let i = 0; i <= 12; i++) {
    const y = -R * 0.8 + (i / 12) * R * 1.6;
    scaleSegs.push([R * 1.02, y, R * 1.02 + (i % 3 === 0 ? 0.16 : 0.08), y]);
  }
  g.add(new THREE.LineSegments(segGeometry(scaleSegs), holoLine(0x4fb8e8, 0.6)));

  anchors.push({
    id: 'radar-title',
    panel: 'radar',
    kind: 'title',
    align: 'center',
    text: 'RISK RADAR',
    local: new THREE.Vector3(0, R + 0.28, 0),
  });
  anchors.push({
    id: 'radar-sub',
    panel: 'radar',
    kind: 'sub',
    align: 'center',
    text: '风险态势雷达',
    local: new THREE.Vector3(0, R + 0.28, 0),
    offsetY: 14,
  });
  anchors.push({
    id: 'radar-foot',
    panel: 'radar',
    kind: 'value',
    align: 'center',
    text: 'COVERAGE 12 区县 · SWEEP 4s',
    local: new THREE.Vector3(0, -R - 0.26, 0),
  });

  return {
    group: g,
    update(t) {
      sweep.rotation.z = t * (Math.PI * 2) / 4;
      front.rotation.z = sweep.rotation.z;
      front.material.opacity = 0.7 + Math.sin(t * 5) * 0.25;
      blips.forEach((b, i) => {
        b.mesh.material.opacity = 0.45 + Math.abs(Math.sin(t * 1.6 + i * 1.3)) * 0.55;
      });
      rings.forEach((r, i) => {
        r.material.opacity = (i === 3 ? 0.7 : 0.32) + Math.sin(t * 1.2 + i) * 0.06;
      });
    },
  };
}

/* ==========================================================
 * 数据导管：把三块面板与盾牌连起来
 * ========================================================== */
function buildConduits(fromPoints, anchors) {
  const g = new THREE.Group();
  g.name = 'fuiConduits';

  const pulses = [];

  fromPoints.forEach(([ax, ay, bx, by], idx) => {
    const pts = [];
    const N = 40;
    for (let i = 0; i <= N; i++) {
      const t = i / N;
      const x = ax + (bx - ax) * t;
      // 末端向盾牌方向弯一点，做成折线
      const y = ay + (by - ay) * t + Math.sin(t * Math.PI) * 0.35 * (idx % 2 ? 1 : -1);
      pts.push(new THREE.Vector3(x, y, 0));
    }
    const line = new THREE.Line(
      new THREE.BufferGeometry().setFromPoints(pts),
      new THREE.LineDashedMaterial({
        color: 0x4fc8f0,
        transparent: true,
        opacity: 0.32,
        dashSize: 0.14,
        gapSize: 0.1,
        blending: THREE.AdditiveBlending,
        depthWrite: false,
      })
    );
    line.computeLineDistances();
    g.add(line);

    // 导管拾取面（沿线的窄条）
    const mid = new THREE.Vector3((ax + bx) / 2, (ay + by) / 2, 0);
    const len = Math.hypot(bx - ax, by - ay);
    const proxy = pickProxy(len, 0.22);
    proxy.position.copy(mid);
    proxy.rotation.z = Math.atan2(by - ay, bx - ax);
    g.add(proxy);

    // 沿线流动的光点
    const pulse = new THREE.Mesh(
      new THREE.PlaneGeometry(0.13, 0.13),
      new THREE.MeshBasicMaterial({
        map: createBlipSprite(),
        color: 0x9beeff,
        transparent: true,
        blending: THREE.AdditiveBlending,
        depthWrite: false,
        opacity: 0.9,
      })
    );
    g.add(pulse);
    pulses.push({ pulse, pts, offset: idx * 0.37 });
  });

  return {
    group: g,
    update(t) {
      pulses.forEach((p) => {
        const k = ((t * 0.22 + p.offset) % 1);
        const i = Math.min(p.pts.length - 1, Math.floor(k * (p.pts.length - 1)));
        p.pulse.position.copy(p.pts[i]);
        p.pulse.material.opacity = 0.9 * (1 - Math.abs(k - 0.5) * 0.8);
      });
    },
  };
}

/* ==========================================================
 * 对外接口
 * ========================================================== */
export function createFui(opts = {}) {
  const group = new THREE.Group();
  group.name = 'fuiGroup';

  const anchors = [];

  const telemetry = buildTelemetry(anchors);
  const status = buildStatusStrip(anchors);
  const radar = buildRadar(anchors);

  // 三个区域的落位（世界坐标，对应画面里三块空区）
  telemetry.group.position.set(-3.68, 3.02, 0.35);
  status.group.position.set(1.32, 5.16, -0.45);
  radar.group.position.set(3.42, 2.72, -0.9);

  const conduits = buildConduits(
    [
      // 左柱 → 盾牌
      [-2.85, 3.02, -1.25, 2.95],
      // 上条 → 盾牌
      [1.32, 4.6, 0.6, 4.3],
      // 雷达 → 盾牌
      [2.32, 2.72, 1.3, 2.8],
    ],
    anchors
  );
  conduits.group.position.copy(telemetry.group.position);
  conduits.group.position.set(0, 0, 0);

  group.add(telemetry.group, status.group, radar.group, conduits.group);

  const info = {
    'fui-telemetry': {
      id: 'fui-telemetry',
      name: '监管运行态势 · Supervision Telemetry',
      eyebrow: 'SUPERVISION / 监管运行',
      desc:
        '左侧的监管运行态势柱，把等级保护监管业务的关键指标以条形计量呈现：' +
        '等级测评项目、监督检查任务、备案审核件、整改闭环率、高风险对象、接入监管单位与在册测评机构。' +
        '填充长度对应实时数值并带轻微采样抖动；高风险对象数超阈值时转绯红，在册机构数不足时转琥珀。',
      specs: [
        ['监测项', '7 项监管业务指标'],
        ['刷新', '实时（连续采样）'],
        ['告警色', '琥珀 / 绯红'],
        ['位置', '舱室左区'],
      ],
    },
    'fui-status': {
      id: 'fui-status',
      name: '监管网格状态 · Supervision Grid',
      eyebrow: 'GRID / 监管网格',
      desc:
        '顶部的监管网格状态条。十颗指示灯汇总展示全市 12 个区县的监管接入情况' +
        '（其中两颗处于数据上报告警闪烁），下方的动态波形是各区县业务回流量的实时采样。' +
        '一眼就能看出整个监管网格是否通畅。',
      specs: [
        ['指示灯', '10 灯阵 · 2 告警'],
        ['波形', '三频叠加业务回流量'],
        ['覆盖', '12 REGIONS'],
        ['位置', '舱室顶区'],
      ],
    },
    'fui-radar': {
      id: 'fui-radar',
      name: '风险态势雷达 · Risk Radar',
      eyebrow: 'RADAR / 风险态势',
      desc:
        '右侧的圆形风险态势雷达。扫描扇形每 4 秒转一圈，扫过的区县会点亮风险回波；' +
        '红色光点代表存在高风险等级保护对象待整改，蓝色光点代表已完成测评备案的系统。' +
        '四圈刻度对应不同的风险量级，右侧竖尺是风险等级读数。',
      specs: [
        ['扫描周期', '4 秒 / 圈'],
        ['覆盖', '全市 12 个区县'],
        ['回波', '5 个风险光点'],
        ['告警色', '绯红'],
      ],
    },
    'fui-conduit': {
      id: 'fui-conduit',
      name: '监管数据回传链路 · Data Conduit',
      eyebrow: 'LINK / 数据回传',
      desc:
        '把三块面板与中央监管中枢连起来的虚线导管。光点沿线流动，代表各区县回传的' +
        '测评、监督检查与备案数据正源源不断汇入监管中枢；流速越快，说明当前汇聚的业务量越大。',
      specs: [
        ['连接', '3 条回传链路'],
        ['流速', '实时汇聚量相关'],
        ['形态', '虚线 + 流动光点'],
        ['去向', '监管中枢'],
      ],
    },
  };

  // 面板挂到拾取列表
  telemetry.group.traverse((o) => {
    if (o.isMesh || o.isLine || o.isLineSegments || o.isLineLoop) o.userData.info = info['fui-telemetry'];
  });
  status.group.traverse((o) => {
    if (o.isMesh || o.isLine || o.isLineSegments || o.isLineLoop) o.userData.info = info['fui-status'];
  });
  radar.group.traverse((o) => {
    if (o.isMesh || o.isLine || o.isLineSegments || o.isLineLoop) o.userData.info = info['fui-radar'];
  });
  conduits.group.traverse((o) => {
    if (o.isMesh || o.isLine || o.isLineSegments || o.isLineLoop) o.userData.info = info['fui-conduit'];
  });

  return {
    group,
    info,
    panels: { telemetry, status, radar },
    conduits,
    /**
     * 把各面板的局部锚点换算成世界坐标
     */
    resolveAnchors() {
      const hosts = {
        telemetry: telemetry.group,
        status: status.group,
        radar: radar.group,
      };
      return anchors.map((a) => {
        const host = hosts[a.panel];
        if (!host) return null;
        host.updateMatrixWorld(true);
        return { ...a, world: host.localToWorld(a.local.clone()) };
      }).filter(Boolean);
    },
    update(t) {
      telemetry.update(t);
      status.update(t);
      radar.update(t);
      conduits.update(t);
    },
  };
}
