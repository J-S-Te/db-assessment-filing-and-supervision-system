import * as THREE from 'three-signin';
import { createCoreEmblemTexture } from './textures.js';
import { createShieldPBR } from './pbr.js';
import { createShieldProfile } from './shieldProfile.js';

/**
 * 盾牌：全部由几何体建模 + 程序化 PBR 贴图，不使用任何外部模型文件。
 *
 * 结构层次（由外到内）：
 *   rimFrame   外圈凸起边框（带倒角，比主体高出一截）
 *   plate      主体装甲板（拉丝金属 + 面板缝 + 划痕 + 边缘磨损）
 *   bolts      六角螺栓 + 垫圈
 *   bezel      中央镜圈（车削出来的倒角环）
 *   recess     中心凹陷
 *   emblem     「防」字徽记
 *   arcs       青 / 红双向弧环
 *   pedestal   底部悬浮支架
 */

/**
 * 自定义 UV 生成器：把盾形轮廓的外接矩形映射到 [0,1]，
 * 这样 PBR 贴图才能正确贴在装甲板上（默认生成器用的是世界坐标，会错位）。
 */
function shieldUVGenerator(bbox) {
  const { minX, maxX, minY, maxY } = bbox;
  const w = maxX - minX || 1;
  const h = maxY - minY || 1;
  const uvOf = (x, y) => [(x - minX) / w, (y - minY) / h];

  return {
    generateTopUV(geometry, vertices, indexA, indexB, indexC) {
      return [
        new THREE.Vector2(...uvOf(vertices[indexA * 3], vertices[indexA * 3 + 1])),
        new THREE.Vector2(...uvOf(vertices[indexB * 3], vertices[indexB * 3 + 1])),
        new THREE.Vector2(...uvOf(vertices[indexC * 3], vertices[indexC * 3 + 1])),
      ];
    },
    generateSideWallUV(geometry, vertices, indexA, indexB, indexC, indexD) {
      // 侧壁沿用正面贴图的投影，看起来像倒角把装甲板包了一圈
      const p = (i) => {
        const [u] = uvOf(vertices[i * 3], vertices[i * 3 + 1]);
        return new THREE.Vector2(u, vertices[i * 3 + 2]);
      };
      return [p(indexA), p(indexB), p(indexC), p(indexD)];
    },
  };
}

/**
 * 用途 装配盾牌模型，并使用已生成或现场生成的原尺寸贴图。
 * 参数 opts 为相机、贴图尺寸和可选的预生成贴图。
 * 返回值 可控制旋转与悬浮的盾牌。
 */
export function createShield(opts = {}) {
  const camera = opts.camera;
  const group = new THREE.Group();
  group.name = 'shieldGroup';

  // 悬浮基准高度（由外部按布局设置），浮动是围绕它做上下呼吸
  let baseY = 0;

  // ---------------- 局部旋转（用户拖拽查看盾牌） ----------------
  // 用四元数而不是欧拉角：拖拽轴跟着相机走，转起来才顺手
  const Y_AXIS = new THREE.Vector3(0, 1, 0);
  const REST_QUAT = new THREE.Quaternion();
  const TURN_QUAT = new THREE.Quaternion();
  const SWAY_QUAT = new THREE.Quaternion();
  const IDENTITY = new THREE.Quaternion();
  const _right = new THREE.Vector3();
  const _up = new THREE.Vector3();
  const _q = new THREE.Quaternion();
  let turning = false;
  // 复原动画：记录松手瞬间的朝向 + 进度
  const RESTORE_DUR = 0.42; // 秒
  let restoreFrom = null;
  let restoreT = 0;

  const { shape, outline, bbox, pbrOptions } = createShieldProfile(opts.textureSize);
  const pbr = opts.pbr ?? createShieldPBR(outline, pbrOptions);

  const armorMaterial = new THREE.MeshPhysicalMaterial({
    map: pbr.map,
    normalMap: pbr.normalMap,
    roughnessMap: pbr.roughnessMap,
    metalnessMap: pbr.metalnessMap,
    aoMap: pbr.aoMap,
    aoMapIntensity: 0.95,
    // 电路分缝 / 走线 / 指示灯：只有这些位置发青光
    emissiveMap: pbr.emissiveMap,
    emissive: 0xffffff,
    emissiveIntensity: 1.55,
    normalScale: new THREE.Vector2(1.2, 1.2),
    color: 0xffffff,
    metalness: 0.68,
    roughness: 1,
    // 清漆 + 虹彩层：让装甲板带一层薄膜干涉的科技感
    clearcoat: 0.6,
    clearcoatRoughness: 0.12,
    iridescence: 0.32,
    iridescenceIOR: 1.38,
    iridescenceThicknessMinimum: 140,
    iridescenceThicknessMaximum: 460,
    envMapIntensity: 2.0,
  });

  // ---------------- 主体装甲板 ----------------
  // 挤出体的 Z 布局（Three.js 开启倒角后的真实几何，三处 Z 值必须对齐，
  // 否则核心、弧环这些部件会被整块埋进装甲板里看不见）：
  //   背面平直面 z = 0
  //   正面平直面 z = DEPTH + BEVEL   ← 正面盖在这里，不是 DEPTH！
  //   背面最深   z = -BEVEL
  const PLATE_DEPTH = 0.22;
  const PLATE_BEVEL = 0.045;
  const PLATE_FRONT = PLATE_DEPTH + PLATE_BEVEL; // 0.265 装甲板正面
  const PLATE_EDGE = PLATE_FRONT;
  const plateGeo = new THREE.ExtrudeGeometry(shape, {
    depth: PLATE_DEPTH,
    bevelEnabled: true,
    bevelThickness: PLATE_BEVEL,
    bevelSize: 0.05,
    bevelOffset: 0,
    bevelSegments: 4,
    curveSegments: 40,
    UVGenerator: shieldUVGenerator(bbox),
  });
  plateGeo.computeVertexNormals();

  const plate = new THREE.Mesh(plateGeo, [armorMaterial, armorMaterial]);
  plate.name = 'shieldBody';
  plate.castShadow = true;
  group.add(plate);

  const plateFront = PLATE_FRONT;

  // ---------------- 外圈凸起边框 ----------------
  const frameShape = new THREE.Shape();
  outline.forEach((p, i) => (i === 0 ? frameShape.moveTo(p.x, p.y) : frameShape.lineTo(p.x, p.y)));
  frameShape.closePath();

  // 内孔：轮廓向内缩 13.5%
  const hole = new THREE.Path();
  const shrink = 0.865;
  const holePts = outline.map((p) => new THREE.Vector2(p.x * shrink, p.y * shrink - 0.02));
  holePts.forEach((p, i) => (i === 0 ? hole.moveTo(p.x, p.y) : hole.lineTo(p.x, p.y)));
  hole.closePath();
  frameShape.holes.push(hole);

  const frameGeo = new THREE.ExtrudeGeometry(frameShape, {
    depth: 0.13,
    bevelEnabled: true,
    bevelThickness: 0.022,
    bevelSize: 0.028,
    bevelOffset: 0,
    bevelSegments: 3,
    curveSegments: 40,
    UVGenerator: shieldUVGenerator(bbox),
  });
  frameGeo.computeVertexNormals();

  const frameMaterial = new THREE.MeshPhysicalMaterial({
    map: pbr.map,
    normalMap: pbr.normalMap,
    roughnessMap: pbr.roughnessMap,
    metalnessMap: pbr.metalnessMap,
    color: 0x9ec8ea,
    metalness: 0.9,
    roughness: 0.5,
    clearcoat: 0.7,
    clearcoatRoughness: 0.08,
    iridescence: 0.22,
    iridescenceIOR: 1.32,
    iridescenceThicknessMinimum: 200,
    iridescenceThicknessMaximum: 520,
    envMapIntensity: 2.6,
  });

  const frame = new THREE.Mesh(frameGeo, [frameMaterial, frameMaterial]);
  frame.name = 'shieldRim';
  // frameGeo 的平直正面在局部 z = depth + bevel = 0.152，
  // 整体上移 0.163 → 正面落在 0.315，比装甲板高出 0.05
  frame.position.z = 0.163;
  frame.castShadow = true;
  group.add(frame);

  // 边框顶面的高光棱线
  const rimLinePts = outline.map((p) => new THREE.Vector3(p.x * 0.998, p.y * 0.998 - 0.01, 0));
  const rimLine = new THREE.LineLoop(
    new THREE.BufferGeometry().setFromPoints(rimLinePts),
    new THREE.LineBasicMaterial({ color: 0x8fe8ff, transparent: true, opacity: 0.85 })
  );
  rimLine.name = 'shieldEdge';
  rimLine.position.z = 0.318;
  group.add(rimLine);

  // ---------------- 六角螺栓 ----------------
  const boltMaterial = new THREE.MeshPhysicalMaterial({
    color: 0x93a9bf,
    metalness: 1,
    roughness: 0.34,
    clearcoat: 0.35,
    envMapIntensity: 1.2,
  });
  const washerMaterial = new THREE.MeshPhysicalMaterial({
    color: 0x27384c,
    metalness: 1,
    roughness: 0.42,
    envMapIntensity: 1.3,
  });

  const boltPositions = [
    [-0.74, 1.0],
    [0.74, 1.0],
    [-0.87, 0.34],
    [0.87, 0.34],
    [-0.55, -0.85],
    [0.55, -0.85],
  ];
  const boltGeo = new THREE.CylinderGeometry(0.055, 0.062, 0.05, 6, 1);
  boltGeo.rotateX(Math.PI / 2);
  const washerGeo = new THREE.CylinderGeometry(0.085, 0.085, 0.014, 16, 1);
  washerGeo.rotateX(Math.PI / 2);

  const bolts = new THREE.Group();
  bolts.name = 'shieldBolts';
  boltPositions.forEach(([bx, by]) => {
    const washer = new THREE.Mesh(washerGeo, washerMaterial);
    washer.position.set(bx, by, plateFront + 0.006);
    bolts.add(washer);
    const bolt = new THREE.Mesh(boltGeo, boltMaterial);
    bolt.position.set(bx, by, plateFront + 0.032);
    bolt.rotation.z = (bx * 7.3 + by * 3.1) % Math.PI;
    bolts.add(bolt);
  });
  group.add(bolts);

  // ---------------- 扫描光带 ----------------
  // 一条青光沿着装甲板自上而下扫过，用盾形轮廓做遮罩，不会溢出到外面
  const scanTex = createScanTexture();
  const scanMask = createShieldMaskTexture(outline, 512);
  const scan = new THREE.Mesh(
    new THREE.PlaneGeometry(2.3, 2.9),
    new THREE.MeshBasicMaterial({
      map: scanTex,
      alphaMap: scanMask,
      color: 0x6fd8ff,
      transparent: true,
      blending: THREE.AdditiveBlending,
      depthWrite: false,
      opacity: 0.55,
    })
  );
  scan.name = 'shieldScan';
  scan.position.z = plateFront + 0.012;
  group.add(scan);

  // ---------------- 中央核心 ----------------
  const coreGroup = new THREE.Group();
  coreGroup.name = 'shieldCore';
  coreGroup.position.z = plateFront + 0.008;
  group.add(coreGroup);

  // 凹陷底座
  const recess = new THREE.Mesh(
    new THREE.CircleGeometry(0.47, 64),
    new THREE.MeshStandardMaterial({
      color: 0x060d18,
      metalness: 0.85,
      roughness: 0.55,
      envMapIntensity: 0.5,
    })
  );
  recess.name = 'shieldRecess';
  recess.position.z = 0.002;
  coreGroup.add(recess);

  // 车削镜圈：真实倒角截面
  const bezelProfile = [
    new THREE.Vector2(0.455, 0.0),
    new THREE.Vector2(0.5, 0.035),
    new THREE.Vector2(0.548, 0.082),
    new THREE.Vector2(0.548, 0.118),
    new THREE.Vector2(0.508, 0.082),
    new THREE.Vector2(0.468, 0.035),
    new THREE.Vector2(0.455, 0.0),
  ];
  const bezel = new THREE.Mesh(
    new THREE.LatheGeometry(bezelProfile, 96),
    new THREE.MeshPhysicalMaterial({
      color: 0x8ea6bf,
      metalness: 1,
      roughness: 0.46,
      clearcoat: 0.28,
      clearcoatRoughness: 0.28,
      envMapIntensity: 0.62,
    })
  );
  bezel.name = 'shieldBezel';
  bezel.rotation.x = Math.PI / 2;
  bezel.position.z = 0.004;
  coreGroup.add(bezel);

  // 「防」字徽记
  const emblemTex = createCoreEmblemTexture('防');
  const emblem = new THREE.Mesh(
    new THREE.CircleGeometry(0.455, 64),
    new THREE.MeshBasicMaterial({
      map: emblemTex,
      transparent: true,
      depthWrite: false,
      opacity: 0.95,
    })
  );
  emblem.name = 'shieldEmblem';
  emblem.position.z = 0.03;
  coreGroup.add(emblem);

  // 核心柔光
  const halo = new THREE.Mesh(
    new THREE.PlaneGeometry(1.7, 1.7),
    new THREE.MeshBasicMaterial({
      map: createCoreGlowSprite(),
      transparent: true,
      blending: THREE.AdditiveBlending,
      depthWrite: false,
      opacity: 0.18,
    })
  );
  halo.name = 'shieldHalo';
  halo.position.z = 0.012;
  coreGroup.add(halo);

  // 玻璃面
  const glass = new THREE.Mesh(
    new THREE.CircleGeometry(0.45, 64),
    new THREE.MeshPhysicalMaterial({
      color: 0xa8dcff,
      metalness: 0,
      // 粗糙度不能太低：近乎镜面的玻璃会把柔光箱整块反射成一坨白斑，
      // 把中间的「防」字直接吃掉
      roughness: 0.22,
      transparent: true,
      opacity: 0.045,
      clearcoat: 0.3,
      clearcoatRoughness: 0.2,
      envMapIntensity: 0.3,
      depthWrite: false,
    })
  );
  glass.name = 'shieldGlass';
  glass.position.z = 0.048;
  coreGroup.add(glass);

  // ---------------- 双色弧环 ----------------
  const arcCyan = new THREE.Mesh(
    new THREE.TorusGeometry(0.62, 0.023, 12, 110, Math.PI * 1.18),
    new THREE.MeshBasicMaterial({
      color: 0x7ceaff,
      transparent: true,
      opacity: 0.95,
      blending: THREE.AdditiveBlending,
      depthWrite: false,
    })
  );
  arcCyan.name = 'shieldArcCyan';
  arcCyan.position.z = 0.33;
  group.add(arcCyan);

  const arcRed = new THREE.Mesh(
    new THREE.TorusGeometry(0.62, 0.023, 12, 110, Math.PI * 0.9),
    new THREE.MeshBasicMaterial({
      color: 0xff5f78,
      transparent: true,
      opacity: 0.9,
      blending: THREE.AdditiveBlending,
      depthWrite: false,
    })
  );
  arcRed.name = 'shieldArcRed';
  arcRed.position.z = 0.33;
  group.add(arcRed);

  // 弧环端点
  const capMat = new THREE.MeshBasicMaterial({ color: 0xe6f9ff });
  const capGeo = new THREE.BoxGeometry(0.05, 0.05, 0.05);
  const cap1 = new THREE.Mesh(capGeo, capMat);
  cap1.position.set(Math.cos(Math.PI * 1.18) * 0.62, Math.sin(Math.PI * 1.18) * 0.62, 0.33);
  group.add(cap1);
  const cap2 = new THREE.Mesh(capGeo, capMat);
  cap2.position.set(Math.cos(Math.PI * 0.9) * 0.62, Math.sin(Math.PI * 0.9) * 0.62, 0.33);
  group.add(cap2);

  // 核心点光源
  const light = new THREE.PointLight(0x6fdcff, 1.9, 9, 2);
  light.position.set(0.28, 0.34, 1.45);
  group.add(light);

  const rim = new THREE.PointLight(0xff4f66, 2.1, 9, 2);
  rim.position.set(0, -1.5, -1.6);
  group.add(rim);

  // ---------------- 悬浮光环 ----------------
  // 注意：六棱「盖子」已移到基座组（属于基座，不跟着盾牌浮动），
  // 这里只留紧贴盾牌尖端的一圈悬浮光环，它本来就是盾牌的一部分
  const pedestalRing = new THREE.Mesh(
    new THREE.TorusGeometry(0.8, 0.026, 10, 90),
    new THREE.MeshBasicMaterial({
      color: 0x46d8ff,
      transparent: true,
      opacity: 0.5,
      blending: THREE.AdditiveBlending,
      depthWrite: false,
    })
  );
  pedestalRing.name = 'shieldPedestalRing';
  pedestalRing.rotation.x = Math.PI / 2;
  pedestalRing.position.y = -1.5;
  group.add(pedestalRing);

  // ---------------- 交互元数据 ----------------
  const bodyInfo = {
    id: 'shield-body',
    name: '防护盾体 · Armor Plating',
    eyebrow: 'STRUCTURE / 主体结构',
    desc:
      '盾体是整个舱室的核心防护结构。外层为深蓝钛合金装甲板，表面经拉丝与阳极氧化处理，' +
      '分布着分块面板缝、细密划痕与边缘磨损痕迹；外圈是一道抬高 7 毫米的倒角边框，' +
      '六颗六角螺栓压住肩部与腰部。装甲板在冷白柔光下呈现低饱和金属质感，避免视觉噪音压过核心信息。',
    specs: [
      ['材质', '钛合金 · 拉丝 + 阳极氧化'],
      ['结构', '主体板 + 凸起倒角外框'],
      ['用途', '监管中枢防护外壳'],
      ['细节', '面板分缝 / 划痕 / 边缘磨损'],
      ['紧固', '6 × 六角螺栓'],
    ],
  };

  const coreInfo = {
    id: 'shield-core',
    name: '「防」字认证核心 · Identity Core',
    eyebrow: 'IDENTITY / 身份核心',
    desc:
      '中央圆形徽记是监管平台的身份认证核心，承载账号、口令与多因子凭证的校验状态。' +
      '外圈是车削加工的镜圈，边缘做了两道倒角；镜圈之内是「防」字蚀刻徽记，' +
      '徽记保持呼吸式明灭：稳定呼吸代表认证通道正常，明灭加快说明正在建立会话，' +
      '外圈红色分段则用于提示存在风险事件或异常登录尝试。',
    specs: [
      ['状态', '在线 · 通道正常'],
      ['镜圈', '车削双倒角环'],
      ['协议', 'OAuth2 / OIDC'],
      ['加密', 'TLS 1.3 · HttpOnly'],
    ],
  };

  const arcInfo = {
    id: 'shield-arc',
    name: '双色监控环 · Monitor Ring',
    eyebrow: 'MONITOR / 实时监控',
    desc:
      '环绕核心的青红双弧实时反映平台安全态势：青段表示认证链路通畅，' +
      '红段表示触发了风险告警或会话拦截。两段弧以相反方向缓慢旋转，' +
      '用于提示系统在持续巡检而不是静止展示。',
    specs: [
      ['青色段', '链路通畅'],
      ['红色段', '告警 / 拦截'],
      ['巡检周期', '持续实时'],
      ['渲染', '加色混合'],
    ],
  };

  const pedestalInfo = {
    id: 'shield-pedestal',
    name: '监管中枢支撑台 · Supervision Pedestal',
    eyebrow: 'SUPPORT / 支撑结构',
    desc:
      '基座中央的六棱发射台，以反重力方式托举监管中枢的外壳，使认证核心悬浮在舱室中心。' +
      '它固定在基座上不随中枢浮动，顶面的青色光环持续旋转，是场景里最直接的「系统仍在工作」的视觉信号。',
    specs: [
      ['形态', '六棱台'],
      ['悬浮方式', '反重力场'],
      ['光环', '恒速旋转'],
      ['负载', '主盾体'],
    ],
  };

  plate.userData.info = bodyInfo;
  frame.userData.info = bodyInfo;
  bolts.children.forEach((b) => (b.userData.info = bodyInfo));
  recess.userData.info = coreInfo;
  recess.userData.focus = true;
  emblem.userData.info = coreInfo;
  emblem.userData.focus = true;
  bezel.userData.info = coreInfo;
  glass.userData.info = coreInfo;
  arcCyan.userData.info = arcInfo;
  arcRed.userData.info = arcInfo;

  // ---------------- 动画 ----------------
  /** 按相机轴向累加一次旋转（世界空间） */
  function applyTurn(dx, dy) {
    if (!camera) return;
    _right.setFromMatrixColumn(camera.matrixWorld, 0).normalize();
    _up.setFromMatrixColumn(camera.matrixWorld, 1).normalize();
    TURN_QUAT.premultiply(_q.setFromAxisAngle(_up, dx * 0.0058));
    TURN_QUAT.premultiply(_q.setFromAxisAngle(_right, dy * 0.0058));
    TURN_QUAT.normalize();
  }

  const api = {
    group,
    plate,
    frame,
    bezel,
    coreGroup,
    emblem,
    halo,
    arcCyan,
    arcRed,
    pedestalRing,
    light,
    /** 设置悬浮基准高度（宽窄布局不同） */
    setBaseY(y) {
      baseY = y;
      group.position.y = y;
    },
    getBaseY() {
      return baseY;
    },
    /** 设置静息朝向（偏航角） */
    setRestY(y) {
      REST_QUAT.setFromAxisAngle(Y_AXIS, y);
    },
    /** 开始局部旋转 */
    beginTurn() {
      turning = true;
      restoreFrom = null;
      restoreT = 0;
    },
    /** 拖拽增量：dx/dy 为像素位移 */
    turnBy(dx, dy) {
      applyTurn(dx, dy);
    },
    /** 结束拖拽：盾牌像掰开的弹簧一样「弹」回原位，但全程不回弹 */
    endTurn() {
      turning = false;
      restoreFrom = TURN_QUAT.clone();
      restoreT = 0;
    },
    isTurning() {
      return turning;
    },
    update(t, dt = 0.016) {
      // ---- 悬浮呼吸 ----
      // 两个不同频率的正弦叠加：单一正弦太机械，像节拍器
      const bob = Math.sin(t * 0.62) * 0.105 + Math.sin(t * 0.93 + 1.2) * 0.028;
      group.position.y = baseY + bob;

      // ---- 松手后自动复原 ----
      // 用强 ease-out（指数衰减）：起手极快、尾巴干脆，
      // 曲线单调不越过终点，所以是「弹回」而不是「来回弹」。
      if (!turning && restoreFrom) {
        restoreT = Math.min(1, restoreT + dt / RESTORE_DUR);
        const k = 1 - Math.pow(2, -10 * restoreT); // easeOutExpo
        TURN_QUAT.copy(restoreFrom).slerp(IDENTITY, k);
        if (restoreT >= 1) {
          TURN_QUAT.identity();
          restoreFrom = null;
        }
      }

      // 姿态合成：用户拖拽 → 静息朝向 → 极轻微摆动
      SWAY_QUAT.setFromEuler(
        new THREE.Euler(Math.sin(t * 0.47) * 0.011, 0, Math.cos(t * 0.39) * 0.013)
      );
      group.quaternion.copy(TURN_QUAT).multiply(REST_QUAT).multiply(SWAY_QUAT);

      // 核心整体不自转：汉字转起来会读不出来，只让弧环转
      coreGroup.rotation.z = 0;
      const breathe = 0.82 + Math.sin(t * 1.8) * 0.1;
      emblem.material.opacity = breathe;
      halo.material.opacity = 0.14 + Math.sin(t * 1.8) * 0.05;
      light.intensity = 1.8 + Math.sin(t * 1.8) * 0.6;
      scanTex.offset.y = -(t * 0.19) % 1;
      scan.material.opacity = 0.42 + Math.sin(t * 1.3) * 0.08;
      arcCyan.rotation.z = t * 0.55;
      arcRed.rotation.z = -t * 0.42;
      pedestalRing.rotation.z = t * 0.9;
    },
  };

  return api;
}

/** 核心柔光贴图（单独画，避免与 textures.js 形成循环依赖） */
function createCoreGlowSprite() {
  const size = 128;
  const canvas = document.createElement('canvas');
  canvas.width = canvas.height = size;
  const ctx = canvas.getContext('2d');
  const g = ctx.createRadialGradient(size / 2, size / 2, 0, size / 2, size / 2, size / 2);
  g.addColorStop(0, 'rgba(130,220,255,0.85)');
  g.addColorStop(0.35, 'rgba(70,160,255,0.26)');
  g.addColorStop(1, 'rgba(30,80,200,0)');
  ctx.fillStyle = g;
  ctx.fillRect(0, 0, size, size);
  const texture = new THREE.CanvasTexture(canvas);
  texture.colorSpace = THREE.SRGBColorSpace;
  return texture;
}

/** 扫描光带贴图：一条会滚动的高亮横带 */
function createScanTexture() {
  const canvas = document.createElement('canvas');
  canvas.width = 8;
  canvas.height = 512;
  const ctx = canvas.getContext('2d');
  const g = ctx.createLinearGradient(0, 0, 0, 512);
  g.addColorStop(0.0, 'rgba(110,215,255,0)');
  g.addColorStop(0.4, 'rgba(110,215,255,0)');
  g.addColorStop(0.455, 'rgba(160,235,255,0.45)');
  g.addColorStop(0.5, 'rgba(225,252,255,0.95)');
  g.addColorStop(0.545, 'rgba(160,235,255,0.45)');
  g.addColorStop(0.6, 'rgba(110,215,255,0)');
  g.addColorStop(1.0, 'rgba(110,215,255,0)');
  ctx.fillStyle = g;
  ctx.fillRect(0, 0, 8, 512);
  const texture = new THREE.CanvasTexture(canvas);
  texture.wrapS = THREE.ClampToEdgeWrapping;
  texture.wrapT = THREE.RepeatWrapping;
  texture.colorSpace = THREE.SRGBColorSpace;
  return texture;
}

/** 盾形遮罩：白 = 盾体内部，黑 = 外部，用来裁剪扫描光带 */
function createShieldMaskTexture(pts, size = 512) {
  const canvas = document.createElement('canvas');
  canvas.width = canvas.height = size;
  const ctx = canvas.getContext('2d');
  ctx.fillStyle = '#000000';
  ctx.fillRect(0, 0, size, size);

  // 扫描带所在平面是 2.3 × 2.9，中心在原点
  const W = 2.3;
  const H = 2.9;
  ctx.fillStyle = '#ffffff';
  ctx.beginPath();
  pts.forEach((p, i) => {
    const x = ((p.x + W / 2) / W) * size;
    const y = (1 - (p.y + H / 2) / H) * size;
    if (i === 0) ctx.moveTo(x, y);
    else ctx.lineTo(x, y);
  });
  ctx.closePath();
  ctx.fill();

  const texture = new THREE.CanvasTexture(canvas);
  texture.colorSpace = THREE.NoColorSpace;
  texture.needsUpdate = true;
  return texture;
}
