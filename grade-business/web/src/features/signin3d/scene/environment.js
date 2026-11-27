import * as THREE from 'three-signin';
import { createStudioEnvTexture } from './pbr.js';

/**
 * 环境：背景、雾、灯光
 *
 * 背景不用 scene.background（会被拉伸变形），而是自己画一张全屏四边形，
 * 用 cover 方式贴图，保证任何屏幕比例下都不变形。
 * 同一张图同时经 PMREM 生成环境贴图，给金属材质提供反射细节。
 */

const BG_VERT = /* glsl */ `
  varying vec2 vUv;
  void main() {
    vUv = uv;
    // 直接输出到 NDC，绕过相机矩阵：无论镜头怎么转，这张图都铺满屏幕
    gl_Position = vec4(position.xy, 1.0, 1.0);
  }
`;

const BG_FRAG = /* glsl */ `
  uniform sampler2D uMap;
  uniform vec2 uScale;
  uniform vec2 uOffset;
  uniform float uTime;
  varying vec2 vUv;

  void main() {
    // cover 适配：保证图片完整覆盖画面且不拉伸
    vec2 uv = (vUv - 0.5) * uScale + 0.5 + uOffset;
    uv = clamp(uv, vec2(0.0), vec2(1.0));

    vec3 col = texture2D(uMap, uv).rgb;

    // 底部轻微加深，让地台与背景衔接更自然
    float v = smoothstep(0.0, 0.55, vUv.y);
    col *= mix(0.55, 1.0, v);

    // 顶部一点点冷色辉光，模拟舱室顶灯
    float top = smoothstep(0.6, 1.0, vUv.y);
    col += vec3(0.02, 0.055, 0.10) * top;

    // 极缓慢的呼吸，避免背景完全静止显得死板
    col *= 0.96 + 0.04 * sin(uTime * 0.35);

    gl_FragColor = vec4(col, 1.0);
  }
`;

export function createEnvironment(renderer, scene, texture, quality = 'high') {
  // ---------------- 背景四边形 ----------------
  const bgMaterial = new THREE.ShaderMaterial({
    uniforms: {
      uMap: { value: texture },
      uScale: { value: new THREE.Vector2(1, 1) },
      uOffset: { value: new THREE.Vector2(0, 0) },
      uTime: { value: 0 },
    },
    vertexShader: BG_VERT,
    fragmentShader: BG_FRAG,
    depthTest: false,
    depthWrite: false,
    toneMapped: false,
  });

  const background = new THREE.Mesh(new THREE.PlaneGeometry(2, 2), bgMaterial);
  background.name = 'backgroundQuad';
  background.frustumCulled = false;
  background.renderOrder = -1000;
  scene.add(background);

  // ---------------- 环境贴图（金属反射） ----------------
  // 用「程序化影棚」而不是太空图来照金属：
  // 金属像不像金属，取决于它反射的光源**有没有形状**。
  // 影棚里的柔光箱会在金属面上拉出一条条长高光，这才是金属的质感来源。
  const pmrem = new THREE.PMREMGenerator(renderer);
  const studioSource = createStudioEnvTexture(quality === 'low' ? 1024 : 2048);
  const envRT = pmrem.fromEquirectangular(studioSource);
  scene.environment = envRT.texture;
  scene.environmentIntensity = 1.55;
  studioSource.dispose();

  // ---------------- 雾 ----------------
  scene.fog = new THREE.FogExp2(0x05101f, quality === 'low' ? 0.016 : 0.021);

  // ---------------- 灯光 ----------------
  const lights = new THREE.Group();
  lights.name = 'lights';

  // 环境光：天空冷蓝，地面带一点红（呼应基座）
  const hemi = new THREE.HemisphereLight(0x33587f, 0x1a060c, 0.34);
  lights.add(hemi);

  // 主光：右上方的冷白键光（同时负责投射接触阴影）
  const key = new THREE.DirectionalLight(0xdcefff, 2.6);
  key.position.set(6.5, 9, 5.5);
  if (quality !== 'low') {
    key.castShadow = true;
    key.shadow.mapSize.set(2048, 2048);
    key.shadow.camera.near = 1;
    key.shadow.camera.far = 32;
    key.shadow.camera.left = -7;
    key.shadow.camera.right = 7;
    key.shadow.camera.top = 8;
    key.shadow.camera.bottom = -5;
    key.shadow.bias = -0.0009;
    key.shadow.normalBias = 0.022;
    key.shadow.radius = 2.2;
  }
  lights.add(key);

  // 补光：左后方的蓝色轮廓
  const fill = new THREE.DirectionalLight(0x3f86c8, 1.2);
  fill.position.set(-7, 3.5, -4);
  lights.add(fill);

  // 警戒光：左后方的红色点光，给基座和盾体背面染色
  const alarm = new THREE.PointLight(0xff4f66, 4.2, 22, 2);
  alarm.position.set(-5.5, 2.6, -4.5);
  lights.add(alarm);

  // 相机侧补光：保证正对观众的金属面永远有造型，不会黑成一片
  const camFill = new THREE.DirectionalLight(0xbcd8f5, 1.15);
  camFill.position.set(1.2, 2.4, 6);
  lights.add(camFill);

  // 青色轮廓光：从左后方勾出装甲板的科技感边缘
  const techRim = new THREE.DirectionalLight(0x59d8ff, 1.5);
  techRim.position.set(-6.5, 3.2, -5.5);
  lights.add(techRim);

  // 底部冷光：托起基座
  const under = new THREE.PointLight(0x2f9fd8, 4.5, 18, 2);
  under.position.set(0, -1.4, 4);
  lights.add(under);

  scene.add(lights);

  const api = {
    background,
    bgMaterial,
    lights,
    key,
    alarm,
    baseOffset: new THREE.Vector2(),
    /**
     * 根据画布尺寸与图片原始比例，更新背景的 cover 缩放
     * @param {number} width  画布宽
     * @param {number} height 画布高
     * @param {number} offsetX 三维内容的横向视口偏移（像素）
     * @param {number} offsetY 三维内容的纵向视口偏移（像素）
     */
    resize(width, height, offsetX = 0, offsetY = 0) {
      const img = texture.image;
      const imgAspect = (img?.width || 16) / (img?.height || 9);
      const canvasAspect = width / Math.max(1, height);
      let sx = 1;
      let sy = 1;
      if (imgAspect > canvasAspect) {
        // 图更宽 → 横向裁切
        sx = canvasAspect / imgAspect;
      } else {
        // 图更高 → 纵向裁切
        sy = imgAspect / canvasAspect;
      }
      bgMaterial.uniforms.uScale.value.set(sx, sy);
      // 背景跟着三维内容一起偏移，构图才不脱节
      api.baseOffset.set(offsetX / width, -offsetY / height);
      bgMaterial.uniforms.uOffset.value.copy(api.baseOffset);
    },
    /**
     * 相机水平旋转时，背景做轻微视差，避免完全静止的「贴纸感」
     */
    setAzimuth(angle) {
      bgMaterial.uniforms.uOffset.value.x =
        api.baseOffset.x + (angle / (Math.PI * 2)) * 0.16;
    },
    update(t) {
      bgMaterial.uniforms.uTime.value = t;
      // 警戒灯缓慢呼吸
      alarm.intensity = 3.8 + Math.sin(t * 0.9) * 1.1;
    },
    dispose() {
      pmrem.dispose();
      envRT.dispose();
      bgMaterial.dispose();
      background.geometry.dispose();
    },
  };

  return api;
}
