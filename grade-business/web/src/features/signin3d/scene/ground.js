import * as THREE from 'three-signin';
import { createDotSprite } from './textures.js';

/**
 * 点阵地面 + 星空
 * 地面用网格状光点铺出透视地台，远端淡出，制造纵深感
 */
export function createGround(quality = 'high') {
  const group = new THREE.Group();
  group.name = 'groundGroup';

  const sprite = createDotSprite('rgba(150,225,255,1)', 'rgba(50,130,220,0)');

  // ---------------- 点阵地面 ----------------
  const spacing = quality === 'low' ? 1.6 : 1.15;
  const maxR = 27;
  const positions = [];
  const colors = [];

  for (let x = -maxR; x <= maxR; x += spacing) {
    for (let z = -maxR; z <= maxR; z += spacing) {
      const d = Math.hypot(x, z);
      if (d < 3.4 || d > maxR) continue;
      // 距离越远越暗，近处偏青
      const fade = 1 - Math.min(1, (d - 3.4) / (maxR - 3.4));
      const jitter = (Math.random() - 0.5) * 0.22;
      positions.push(x + jitter, 0, z + jitter);
      const b = 0.16 + fade * 0.84;
      colors.push(0.22 * b, 0.72 * b, 1.0 * b);
    }
  }

  const geo = new THREE.BufferGeometry();
  geo.setAttribute('position', new THREE.Float32BufferAttribute(positions, 3));
  geo.setAttribute('color', new THREE.Float32BufferAttribute(colors, 3));

  const mat = new THREE.PointsMaterial({
    map: sprite,
    size: 0.19,
    sizeAttenuation: true,
    vertexColors: true,
    transparent: true,
    opacity: 0.85,
    depthWrite: false,
    blending: THREE.AdditiveBlending,
  });
  const dots = new THREE.Points(geo, mat);
  dots.name = 'groundDots';
  group.add(dots);

  // 地面之上的一层网格（辅助线）
  const grid = new THREE.GridHelper(54, 54, 0x1f5f92, 0x123c60);
  grid.name = 'groundGrid';
  grid.position.y = -0.02;
  grid.material.transparent = true;
  grid.material.opacity = 0.12;
  grid.material.depthWrite = false;
  group.add(grid);

  // ---------------- 星空 ----------------
  const starCount = quality === 'low' ? 700 : 1500;
  const starPos = new Float32Array(starCount * 3);
  const starSize = new Float32Array(starCount);
  for (let i = 0; i < starCount; i++) {
    // 在半球壳上均匀分布
    const u = Math.random();
    const v = Math.random();
    const theta = u * Math.PI * 2;
    const phi = Math.acos(2 * v - 1);
    const r = 55 + Math.random() * 35;
    starPos[i * 3] = r * Math.sin(phi) * Math.cos(theta);
    starPos[i * 3 + 1] = Math.abs(r * Math.cos(phi)) * 0.8 + 4;
    starPos[i * 3 + 2] = r * Math.sin(phi) * Math.sin(theta);
    starSize[i] = 0.25 + Math.random() * 0.85;
  }
  const starGeo = new THREE.BufferGeometry();
  starGeo.setAttribute('position', new THREE.BufferAttribute(starPos, 3));
  starGeo.setAttribute('size', new THREE.BufferAttribute(starSize, 1));
  const stars = new THREE.Points(
    starGeo,
    new THREE.PointsMaterial({
      color: 0xcfe6ff,
      map: createDotSprite('rgba(255,255,255,1)', 'rgba(180,220,255,0)'),
      size: 0.5,
      sizeAttenuation: true,
      transparent: true,
      opacity: 0.75,
      depthWrite: false,
      blending: THREE.AdditiveBlending,
    })
  );
  stars.name = 'stars';
  group.add(stars);

  const info = {
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

  dots.userData.info = info;
  grid.userData.info = info;

  const api = {
    group,
    dots,
    stars,
    update(t) {
      // 地面光点做极缓慢的呼吸波浪
      mat.opacity = 0.72 + Math.sin(t * 0.8) * 0.1;
      stars.rotation.y = t * 0.004;
    },
  };

  return api;
}
