import * as THREE from 'three-signin';
import { OrbitControls } from 'three-signin/examples/jsm/controls/OrbitControls.js';
import { EffectComposer } from 'three-signin/examples/jsm/postprocessing/EffectComposer.js';
import { RenderPass } from 'three-signin/examples/jsm/postprocessing/RenderPass.js';
import { UnrealBloomPass } from 'three-signin/examples/jsm/postprocessing/UnrealBloomPass.js';
import { OutputPass } from 'three-signin/examples/jsm/postprocessing/OutputPass.js';

import spaceTextureUrl from '../../../assets/signin-space.webp';
import { createEnvironment } from './environment.js';
import { createGround } from './ground.js';
import { createBase } from './base.js';
import { createShield } from './shield.js';
import { createOrbit } from './orbit.js';
import { createEnergy } from './energy.js';
import { createFui } from './fui.js';
import { prepareMaterials, uploadMaterials } from './materials.js';

/**
 * 场景总装：渲染器 / 相机 / 控制器 / 后期 / 拾取
 * 对外只暴露一个 stage 对象，main.js 负责把它和界面连起来。
 */

const TIERS = {
  // renderScale = 每个 CSS 像素用几个物理像素去渲染（超采样倍率）
  // 在 1920×1080 的窗口下，renderScale=4 就是实打实的 7680×4320 = 8K
  ultra: { renderScale: 4, bloom: true, ground: 'high', bloomStrength: 0.42, msaa: 4, textureSize: 4096, shadows: true },
  high: { renderScale: 2, bloom: true, ground: 'high', bloomStrength: 0.42, msaa: 4, textureSize: 2048, shadows: true },
  medium: { renderScale: 1.5, bloom: true, ground: 'high', bloomStrength: 0.4, msaa: 2, textureSize: 1024, shadows: false },
  low: { renderScale: 1, bloom: false, ground: 'low', bloomStrength: 0, msaa: 0, textureSize: 1024, shadows: false },
};

/** 根据设备情况自动选择画质档位 */
export function detectTier() {
  const params = new URLSearchParams(location.search);
  const forced = params.get('quality');
  if (Object.hasOwn(TIERS, forced)) return forced;

  // 支持 ?res=1|2|3|4 直接指定渲染倍率
  const res = parseFloat(params.get('res'));
  if (res >= 1 && res <= 4) {
    return res >= 3.5 ? 'ultra' : res >= 1.75 ? 'high' : res >= 1.25 ? 'medium' : 'low';
  }

  const dpr = window.devicePixelRatio || 1;
  const w = window.innerWidth;
  const h = window.innerHeight;
  const pixels = w * h * dpr * dpr;
  const coarse = window.matchMedia('(pointer: coarse)').matches;

  if (coarse && pixels > 2.6e6) return 'low';
  if (pixels > 8e6) return 'medium';
  return 'high';
}

/**
 * 用途 初始化登录页的三维渲染器，并在加载失败时释放绘图上下文。
 * 参数 canvas 为登录页画布，options 为画质档位与加载取消信号。
 * 返回值 已初始化的场景，或加载错误。
 */
export async function createStage(canvas, options = {}) {
  const tier = options.tier || detectTier();
  const cfg = TIERS[tier];
  let materials;
  let renderer;
  let stage;
  try {
    renderer = new THREE.WebGLRenderer({
      canvas,
      antialias: false, // 抗锯齿交给 composer 的 MSAA 渲染目标
      alpha: false,
      powerPreference: 'high-performance',
      stencil: false,
    });
    materials = await prepareMaterials(cfg.textureSize, options.signal);
    stage = await buildStage(canvas, renderer, { tier, cfg, materials, signal: options.signal });
    // 预编译与第一帧相同的渲染目标，支持并行编译的浏览器可在等待期间处理输入。
    renderer.setRenderTarget(stage.composer?.readBuffer ?? null);
    const shaders = renderer.compileAsync(stage.scene, stage.camera);
    renderer.setRenderTarget(null);
    await shaders;
    if (materials) await uploadMaterials(renderer, materials.textures, options.signal);
    options.signal?.throwIfAborted();
    return stage;
  } catch (error) {
    if (stage) stage.dispose();
    else {
      materials?.dispose();
      renderer?.dispose();
      renderer?.forceContextLoss();
    }
    throw error;
  }
}

/**
 * 用途 装配原型中的场景、后期处理和拾取交互。
 * 参数 canvas、renderer 为目标画布和渲染器，配置包含画质、材质和取消信号。
 * 返回值 可暂停、恢复和释放的场景。
 */
async function buildStage(canvas, renderer, { tier, cfg, materials, signal }) {
  // 自动摆动状态（必须在 reframeForLayout 之前声明，否则会踩 TDZ）
  let swayOffset = 0;
  let autoPaused = false;
  // stage 也要提前声明：resize() 会用到它
  let stage = null;

  const gl = renderer.getContext();
  const maxSide = Math.min(renderer.capabilities.maxTextureSize, gl.getParameter(gl.MAX_RENDERBUFFER_SIZE));
  let renderScale = cfg.renderScale;
  const canvasWidth = canvas.clientWidth || window.innerWidth;
  const canvasHeight = canvas.clientHeight || window.innerHeight;
  let bufferScale = safeScale(canvasWidth, canvasHeight);
  renderer.setPixelRatio(bufferScale);
  renderer.setSize(canvasWidth, canvasHeight, false);
  renderer.outputColorSpace = THREE.SRGBColorSpace;
  renderer.toneMapping = THREE.ACESFilmicToneMapping;
  renderer.toneMappingExposure = 1.22;
  renderer.setClearColor(0x04070f, 1);
  renderer.shadowMap.enabled = !!cfg.shadows;
  renderer.shadowMap.type = THREE.PCFShadowMap; // r187 起 PCFSoftShadowMap 已被移除

  // ---------------- 场景 ----------------
  const scene = new THREE.Scene();

  // 背景贴图
  const spaceTex = await new Promise((resolve, reject) => {
    const loader = new THREE.TextureLoader();
    loader.load(spaceTextureUrl, resolve, undefined, reject);
  });
  if (signal?.aborted) {
    spaceTex.dispose();
    signal.throwIfAborted();
  }
  spaceTex.colorSpace = THREE.SRGBColorSpace;

  const environment = createEnvironment(renderer, scene, spaceTex, cfg.ground);

  // ---------------- 相机（要早于盾牌创建：盾牌局部旋转要用它取轴向） ----------------
  const camera = new THREE.PerspectiveCamera(46, 16 / 9, 0.1, 260);
  camera.position.set(1.1, 3.35, 10.4);

  // ---------------- 场景内容 ----------------
  const ground = createGround(cfg.ground);
  const base = createBase({
    textureSize: cfg.textureSize, deckPbr: materials?.deck, sidePbr: materials?.side,
  });
  const shield = createShield({ textureSize: cfg.textureSize, camera, pbr: materials?.shield });
  const orbit = createOrbit();
  const energy = createEnergy();
  // FUI 全息仪表：填满舱室里三块空区
  const fui = createFui();

  // 盾体悬浮高度（基准值，浮动由 shield 自己驱动）
  shield.setBaseY(2.95);
  shield.setRestY(-0.16);
  shield.group.scale.setScalar(1.14);
  const shieldBaseScale = 1.14;

  const content = new THREE.Group();
  content.name = 'content';
  content.add(ground.group, base.group, energy.group, orbit.group, shield.group, fui.group);
  scene.add(content);

  // 基座下方的接地阴影（伪 AO，让基座「坐」在地面上）
  const aoCanvas = document.createElement('canvas');
  aoCanvas.width = aoCanvas.height = 256;
  const aoCtx = aoCanvas.getContext('2d');
  const aoGrad = aoCtx.createRadialGradient(128, 128, 8, 128, 128, 128);
  aoGrad.addColorStop(0, 'rgba(0,0,0,0.42)');
  aoGrad.addColorStop(0.55, 'rgba(0,0,0,0.14)');
  aoGrad.addColorStop(1, 'rgba(0,0,0,0)');
  aoCtx.fillStyle = aoGrad;
  aoCtx.fillRect(0, 0, 256, 256);
  const aoTex = new THREE.CanvasTexture(aoCanvas);
  const ao = new THREE.Mesh(
    new THREE.PlaneGeometry(8.4, 8.4),
    new THREE.MeshBasicMaterial({
      map: aoTex,
      transparent: true,
      depthWrite: false,
      opacity: 0.9,
    })
  );
  ao.rotation.x = -Math.PI / 2;
  ao.position.y = 0.015;
  scene.add(ao);

  // 地面大面积拾取面（不可见，只负责响应点击）
  const pickPlane = new THREE.Mesh(
    new THREE.CircleGeometry(18, 48),
    new THREE.MeshBasicMaterial({ visible: false })
  );
  pickPlane.rotation.x = -Math.PI / 2;
  pickPlane.position.y = 0.02;
  pickPlane.userData.info = {
    id: 'ground',
    name: '感知地台 · Sensor Deck',
    eyebrow: 'PERCEPTION / 感知网络',
    desc:
      '由密集光点铺成的地台代表平台覆盖范围之内的感知与监测节点。' +
      '每个光点都是一个采集端，靠近基座的部分密度更高，代表核心区域的监测精度更高。' +
      '外围逐渐稀疏并淡出，暗示监测范围随距离衰减。',
    specs: [
      ['节点', '≈ 2000 个光点'],
      ['网格间距', '1.15 单位'],
      ['衰减半径', '27 单位'],
      ['关联', '全量资产监测'],
    ],
  };
  scene.add(pickPlane);

  const controls = new OrbitControls(camera, canvas);
  controls.enableDamping = true;
  controls.dampingFactor = 0.055;
  controls.enablePan = false;
  controls.rotateSpeed = 0.62;
  controls.zoomSpeed = 0.78;
  controls.minDistance = 4.6;
  controls.maxDistance = 19;
  controls.minPolarAngle = 0.22;
  controls.maxPolarAngle = 1.5;
  controls.target.set(0, 2.25, 0);
  // 不用 controls.autoRotate：它会一路转到场景背面，构图全垮
  // 改成「正面附近缓慢摆动 + 停手后缓缓回正」，见 updateAutoMotion()
  controls.autoRotate = false;

  // ---------------- 后期 ----------------
  let composer = null;
  let bloomPass = null;
  if (cfg.bloom) {
    // 自带 MSAA 的渲染目标：真正的多重采样抗锯齿（而不是 FXAA 那种糊边）
    const size = renderer.getDrawingBufferSize(new THREE.Vector2());
    const rt = new THREE.WebGLRenderTarget(size.x, size.y, {
      type: THREE.HalfFloatType,
      samples: cfg.msaa,
    });
    composer = new EffectComposer(renderer, rt);
    composer.addPass(new RenderPass(scene, camera));
    bloomPass = new UnrealBloomPass(
      new THREE.Vector2(size.x, size.y),
      cfg.bloomStrength,
      0.72,
      0.82
    );
    composer.addPass(bloomPass);
    composer.addPass(new OutputPass());
  }

  // ---------------- 拾取 ----------------
  const interactables = [];
  [base.group, shield.group, orbit.group, energy.group, fui.group].forEach((g) => {
    g.traverse((obj) => {
      if ((obj.isMesh || obj.isLine || obj.isPoints) && obj.userData.info) {
        interactables.push(obj);
      }
    });
  });
  interactables.push(pickPlane);

  const raycaster = new THREE.Raycaster();
  raycaster.params.Line.threshold = 0.32;
  raycaster.params.Points.threshold = 0.3;

  const pointer = new THREE.Vector2();

  function pick(clientX, clientY) {
    const rect = canvas.getBoundingClientRect();
    pointer.x = ((clientX - rect.left) / rect.width) * 2 - 1;
    pointer.y = -((clientY - rect.top) / rect.height) * 2 + 1;
    raycaster.setFromCamera(pointer, camera);
    const hits = raycaster.intersectObjects(interactables, false);
    if (!hits.length) return null;
    // 面板的「透明拾取面」优先级最低：实体物体（盾牌、基座…）永远优先，
    // 只有当画面上确实只有面板时才选中面板
    const solid = hits.find((h) => !h.object.userData.soft);
    return solid || hits[0];
  }

  // ---------------- 布局适配 ----------------
  let layout = 'wide';
  let lastLayout = null;
  let viewOffset = { x: 0, y: 0 };

  function applyViewOffset(width, height) {
    if (layout === 'wide') {
      // 宽屏：把场景整体推到画面左侧，右侧留给登录卡
      viewOffset = { x: width * 0.145, y: height * 0.02 };
    } else {
      // 窄屏：三维只占上半屏，场景整体上移，给下方的登录卡让位
      viewOffset = { x: 0, y: height * 0.17 };
    }
    camera.setViewOffset(width, height, viewOffset.x, viewOffset.y, width, height);
    camera.aspect = width / Math.max(1, height);
    camera.updateProjectionMatrix();
  }

  /**
   * 宽窄布局切换时重新取景。
   * 只在布局真正发生变化时执行，避免用户拖动视角时被强行拉回。
   */
  function reframeForLayout() {
    const narrow = layout === 'narrow';
    controls.target.set(0, narrow ? 2.05 : 2.25, 0);
    const dir = new THREE.Vector3().subVectors(camera.position, controls.target);
    if (dir.lengthSq() < 0.001) dir.set(0, 0.12, 1);
    dir.normalize();
    const dist = narrow ? 15.8 : 10.5;
    camera.position.copy(controls.target).add(dir.multiplyScalar(dist));
    swayOffset = 0;
    autoPaused = false;
    controls.minDistance = narrow ? 7.5 : 4.6;
    controls.maxDistance = narrow ? 22 : 19;
    shield.group.scale.setScalar(narrow ? shieldBaseScale * 0.86 : shieldBaseScale);
    shield.setBaseY(narrow ? 2.72 : 2.95);
    controls.update();
  }

  /**
   * 用途 按设备上限和 8K 像素预算计算实际渲染倍率。
   * 参数 width、height 为画布的 CSS 尺寸。
   * 返回值 可分配的绘图缓冲区倍率。
   */
  function safeScale(width, height) {
    return Math.min(renderScale, maxSide / Math.max(width, height), Math.sqrt(33177600 / (width * height)));
  }

  /**
   * 用途 同步画布、后期处理与响应式构图。
   * 参数 无，使用画布可见尺寸及当前画质。
   * 返回值 无。
   */
  function resize() {
    const width = canvas.clientWidth || window.innerWidth;
    const height = canvas.clientHeight || window.innerHeight;
    layout = width <= 900 || height <= 620 ? 'narrow' : 'wide';

    const nextScale = safeScale(width, height);
    if (nextScale !== bufferScale) {
      bufferScale = nextScale;
      renderer.setPixelRatio(bufferScale);
      composer?.setPixelRatio(bufferScale);
    }
    renderer.setSize(width, height, false);
    composer?.setSize(width, height);
    applyViewOffset(width, height);
    environment.resize(width, height, viewOffset.x, viewOffset.y);
    // 画布尺寸变了，通知外部刷新「实际输出分辨率」显示
    stage?.onResize?.();
    // 窄屏放不下三块全息仪表，直接隐藏
    fui.group.visible = layout !== 'narrow';
    if (layout !== lastLayout) {
      lastLayout = layout;
      reframeForLayout();
    }
    controls.update();
  }

  // ---------------- 事件 ----------------
  let pointerDown = null;
  let idleTimer = 0;

  // 鼠标分区：点在盾牌上 → 只转盾牌；点在别处 → 转整个场景
  const shieldDrag = { active: false, lastX: 0, lastY: 0 };

  /** 判断某个屏幕点是否落在盾牌上 */
  function pickShield(cx, cy) {
    const rect = canvas.getBoundingClientRect();
    pointer.x = ((cx - rect.left) / rect.width) * 2 - 1;
    pointer.y = -((cy - rect.top) / rect.height) * 2 + 1;
    raycaster.setFromCamera(pointer, camera);
    const hits = raycaster.intersectObject(shield.group, true);
    return hits.length ? hits[0] : null;
  }

  function endShieldDrag() {
    if (!shieldDrag.active) return;
    shieldDrag.active = false;
    controls.enabled = true;
    shield.endTurn();
  }

  /**
   * 用途 在按下鼠标后启动拖动，命中盾牌时接管为局部旋转。
   * 参数 e 为画布指针事件。
   * 返回值 无。
   */
  function onPointerDown(e) {
    pointerDown = { x: e.clientX, y: e.clientY, t: performance.now() };
    autoPaused = true;
    canvas.classList.add('is-grabbing');
    idleTimer = 0;

    // 命中盾牌 → 接管成「盾牌局部旋转」，场景镜头不再跟着转
    if (pickShield(e.clientX, e.clientY)) {
      shieldDrag.active = true;
      shieldDrag.lastX = e.clientX;
      shieldDrag.lastY = e.clientY;
      controls.enabled = false;
      shield.beginTurn();
    }
  }

  /**
   * 用途 结束拖动，短按命中核心部件时保留聚焦操作。
   * 参数 e 为释放指针的事件。
   * 返回值 无。
   */
  function onPointerUp(e) {
    canvas.classList.remove('is-grabbing');
    endShieldDrag();
    if (!pointerDown) return;
    const moved = Math.hypot(e.clientX - pointerDown.x, e.clientY - pointerDown.y);
    const dt = performance.now() - pointerDown.t;
    pointerDown = null;
    if (moved < 7 && dt < 700 && !shieldDrag.active) {
      const hit = pick(e.clientX, e.clientY);
      if (hit) {
        stage.onSelect?.(hit.object.userData.info, hit.point, hit.object);
        return;
      }
      stage.onSelect?.(null);
      return;
    }
    idleTimer = 0;
  }

  /**
   * 用途 根据正在进行的鼠标拖动更新盾牌角度。
   * 参数 e 为当前指针位置。
   * 返回值 无。
   */
  function onPointerMove(e) {
    if (!shieldDrag.active || e.pointerType === 'touch') return;
    const dx = e.clientX - shieldDrag.lastX;
    const dy = e.clientY - shieldDrag.lastY;
    shieldDrag.lastX = e.clientX;
    shieldDrag.lastY = e.clientY;
    shield.turnBy(dx, dy);
  }

  function onWheel() {
    autoPaused = true;
    idleTimer = 0;
  }

  canvas.addEventListener('pointerdown', onPointerDown);
  window.addEventListener('pointerup', onPointerUp);
  canvas.addEventListener('pointermove', onPointerMove);
  canvas.addEventListener('pointerleave', endShieldDrag);
  canvas.addEventListener('wheel', onWheel, { passive: true });

  const ro = new ResizeObserver(resize);
  ro.observe(canvas);
  resize();

  // ---------------- 自动摆动（替代 autoRotate） ----------------
  // 目标：镜头始终停在「正对观众」的构图附近，
  // 但用户手动转 360° 依然完全自由。
  const SWAY_AMP = 0.26; // ±15°
  const SWAY_FREQ = 0.055; // 约 57 秒一个来回
  const Y_AXIS = new THREE.Vector3(0, 1, 0);

  function rotateCameraAroundTarget(delta) {
    if (Math.abs(delta) < 1e-6) return;
    const off = camera.position.clone().sub(controls.target);
    off.applyAxisAngle(Y_AXIS, delta);
    camera.position.copy(controls.target).add(off);
  }

  function updateAutoMotion(t, dt) {
    if (autoPaused) return;
    // 先把方位角缓缓拉回正面
    const az = controls.getAzimuthalAngle();
    if (Math.abs(az) > 0.0015) {
      rotateCameraAroundTarget(-az * Math.min(1, dt * 0.55));
      // 回正过程中同步摆动相位，避免下一帧跳变
      swayOffset = Math.sin(t * SWAY_FREQ * Math.PI * 2) * SWAY_AMP;
      return;
    }
    // 再叠加小幅左右摆动
    const target = Math.sin(t * SWAY_FREQ * Math.PI * 2) * SWAY_AMP;
    rotateCameraAroundTarget(target - swayOffset);
    swayOffset = target;
  }

  // ---------------- 渲染循环 ----------------
  // 不用 THREE.Clock（r187 已废弃），直接用 performance.now 计时
  let lastTime = performance.now();
  let elapsed = 0;
  let raf = 0;
  let running = false;

  /**
   * 用途 刷新三维场景和跟随相机的标签，可按浏览器偏好减少自动动效。
   * 参数 无，使用当前场景状态。
   * 返回值 无。
   */
  function tick() {
    if (!running) return;
    raf = requestAnimationFrame(tick);
    const now = performance.now();
    const dt = Math.min(0.05, (now - lastTime) / 1000);
    lastTime = now;
    if (!stage.reducedMotion) elapsed += dt;
    const t = elapsed;

    environment.update(t);
    ground.update(t);
    base.update(t);
    shield.update(t, dt);
    orbit.update(t, stage.reducedMotion ? 0 : dt);
    energy.update(t, stage.reducedMotion ? 0 : dt);
    fui.update(t);
    if (!stage.reducedMotion) updateAutoMotion(t, dt);
    environment.setAzimuth(controls.getAzimuthalAngle());

    // 闲置 5 秒后恢复自动摆动
    if (autoPaused && !pointerDown) {
      idleTimer += dt;
      if (idleTimer > 5) {
        autoPaused = false;
        idleTimer = 0;
      }
    }
    controls.update();

    if (composer) composer.render();
    else renderer.render(scene, camera);
    stage.onFrame?.();
  }

  stage = {
    tier,
    renderer,
    scene,
    camera,
    controls,
    composer,
    bloomPass,
    interactables,
    fui,
    shield,
    onResize: null,
    onFrame: null,
    reducedMotion: false,
    // 供登录页挂载回调
    onSelect: null,
    // 用途 启动或恢复渲染。
    // 参数 无。
    // 返回值 无。
    start() {
      if (!raf) {
        running = true;
        lastTime = performance.now();
        tick();
      }
    },
    stop() {
      running = false;
      cancelAnimationFrame(raf);
      raf = 0;
    },
    resize,
    /** 运行时切换渲染倍率（1 / 1.5 / 2 / 4） */
    setResolution(scale) {
      renderScale = scale;
      resize();
    },
    getResolution() {
      return renderScale;
    },
    /**
     * 相机聚焦：只有被标记 userData.focus 的部件（比如「防」字核心）才推近，
     * 否则点远处的 FUI 面板会一下把镜头拉过头，整个构图散掉。
     */
    focus(point, object) {
      if (!point || !object || !object.userData.focus) return;
      const dir = new THREE.Vector3().subVectors(camera.position, controls.target).normalize();
      const want = controls.target.clone().add(dir.multiplyScalar(Math.max(controls.minDistance, 6.6)));
      camera.position.lerp(want, 0.45);
      controls.update();
    },
    // 用途 在退出登录页时释放监听器、图形资源及绘图上下文。
    // 参数 无。
    // 返回值 无。
    dispose() {
      running = false;
      cancelAnimationFrame(raf);
      ro.disconnect();
      canvas.removeEventListener('pointerdown', onPointerDown);
      window.removeEventListener('pointerup', onPointerUp);
      canvas.removeEventListener('pointermove', onPointerMove);
      canvas.removeEventListener('pointerleave', endShieldDrag);
      canvas.removeEventListener('wheel', onWheel);
      controls.dispose();
      if (composer) {
        composer.passes.forEach(pass => pass.dispose());
        composer.dispose();
      }
      environment.dispose();
      spaceTex.dispose();
      materials?.dispose();
      renderer.dispose();
      renderer.forceContextLoss();
    },
  };

  return stage;
}
