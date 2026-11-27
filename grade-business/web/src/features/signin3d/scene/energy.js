import * as THREE from 'three-signin';
import { createEnergyStripTexture, createBeamTexture, createDotSprite } from './textures.js';

/**
 * 能量光带：环绕基座流动的蓝色光幕 + 垂直光柱
 * 使用加色混合 + 滚动条纹贴图，形成「能量在传输」的观感
 */
export function createEnergy() {
  const group = new THREE.Group();
  group.name = 'energyGroup';

  const stripTex = createEnergyStripTexture();

  const bands = [];

  const specs = [
    { radius: 2.95, y: 1.02, amp: 0.16, freq: 3, width: 0.2, color: 0x3ec8ff, opacity: 0.42, speed: 0.085 },
    { radius: 3.45, y: 1.42, amp: 0.22, freq: 2, width: 0.14, color: 0x6fdcff, opacity: 0.34, speed: -0.055 },
    { radius: 2.55, y: 0.62, amp: 0.12, freq: 4, width: 0.12, color: 0x2a9be0, opacity: 0.36, speed: 0.12 },
  ];

  specs.forEach((spec, idx) => {
    const SEG = 220;
    const positions = new Float32Array((SEG + 1) * 2 * 3);
    const uvs = new Float32Array((SEG + 1) * 2 * 2);
    const indices = [];

    for (let i = 0; i <= SEG; i++) {
      const t = i / SEG;
      const a = t * Math.PI * 2;
      const r = spec.radius + Math.sin(a * spec.freq + idx * 1.7) * spec.amp;
      const y = spec.y + Math.sin(a * spec.freq * 1.35 + idx) * spec.amp * 0.5;
      const x = Math.cos(a) * r;
      const z = Math.sin(a) * r;

      const o = i * 6;
      positions[o] = x;
      positions[o + 1] = y - spec.width / 2;
      positions[o + 2] = z;
      positions[o + 3] = x;
      positions[o + 4] = y + spec.width / 2;
      positions[o + 5] = z;

      const u = i * 4;
      uvs[u] = t * 6;
      uvs[u + 1] = 0;
      uvs[u + 2] = t * 6;
      uvs[u + 3] = 1;

      if (i < SEG) {
        const v = i * 2;
        indices.push(v, v + 1, v + 2, v + 1, v + 3, v + 2);
      }
    }

    const geo = new THREE.BufferGeometry();
    geo.setAttribute('position', new THREE.BufferAttribute(positions, 3));
    geo.setAttribute('uv', new THREE.BufferAttribute(uvs, 2));
    geo.setIndex(indices);
    geo.computeVertexNormals();

    const tex = stripTex.clone();
    tex.needsUpdate = true;
    tex.wrapS = THREE.RepeatWrapping;
    tex.wrapT = THREE.ClampToEdgeWrapping;
    tex.repeat.set(1, 1);

    const mat = new THREE.MeshBasicMaterial({
      map: tex,
      color: spec.color,
      transparent: true,
      opacity: spec.opacity,
      side: THREE.DoubleSide,
      blending: THREE.AdditiveBlending,
      depthWrite: false,
    });

    const mesh = new THREE.Mesh(geo, mat);
    mesh.name = `energyBand${idx}`;
    group.add(mesh);

    bands.push({ mesh, tex, spec });
  });

  // ---------------- 垂直光柱（基座 → 盾牌） ----------------
  const beamTex = createBeamTexture();
  const beam = new THREE.Mesh(
    new THREE.CylinderGeometry(1.15, 1.75, 3.4, 48, 1, true),
    new THREE.MeshBasicMaterial({
      map: beamTex,
      color: 0x58c8ff,
      transparent: true,
      opacity: 0.09,
      side: THREE.DoubleSide,
      blending: THREE.AdditiveBlending,
      depthWrite: false,
    })
  );
  beam.name = 'energyBeam';
  beam.position.y = 2.1;
  group.add(beam);

  // 光柱内的上升光粒
  const sparkCount = 160;
  const sparkPos = new Float32Array(sparkCount * 3);
  const sparkSpeed = new Float32Array(sparkCount);
  const sparkSize = new Float32Array(sparkCount);
  for (let i = 0; i < sparkCount; i++) {
    sparkPos[i * 3] = 0;
    sparkPos[i * 3 + 1] = 0;
    sparkPos[i * 3 + 2] = 0;
    sparkSpeed[i] = 0.28 + Math.random() * 0.55;
    sparkSize[i] = 0.03 + Math.random() * 0.06;
  }
  const sparkGeo = new THREE.BufferGeometry();
  sparkGeo.setAttribute('position', new THREE.BufferAttribute(sparkPos, 3));
  sparkGeo.setAttribute('size', new THREE.BufferAttribute(sparkSize, 1));
  const sparkMat = new THREE.PointsMaterial({
    color: 0xbdf0ff,
    map: createDotSprite('rgba(200,245,255,1)', 'rgba(80,180,255,0)'),
    size: 0.1,
    sizeAttenuation: true,
    transparent: true,
    opacity: 0.6,
    depthWrite: false,
    blending: THREE.AdditiveBlending,
  });
  const sparks = new THREE.Points(sparkGeo, sparkMat);
  sparks.name = 'energySparks';
  group.add(sparks);

  const info = {
    id: 'energy',
    name: '能量传输光带 · Energy Conduit',
    eyebrow: 'TRANSMISSION / 传输链路',
    desc:
      '环绕基座的三层蓝色光带代表认证数据在客户端与平台之间流动。' +
      '光带上的条纹以不同方向滚动，方向一致代表数据同向流动；' +
      '一旦出现逆向流光，通常意味着有重复请求或重放攻击在发生。' +
      '中央的垂直光柱则是舱室能量主干，为悬浮的盾体供能。',
    specs: [
      ['光带', '3 层 · 反向滚动'],
      ['光柱', '3.4 单位 · 上升光粒 160'],
      ['混合', '加色混合 Additive'],
      ['含义', '数据流向可视化'],
    ],
  };

  // 只有三条实体光带可拾取；
  // 光柱与上升光粒是体积效果，不参与拾取，否则会挡住盾体的射线
  bands.forEach((b) => {
    b.mesh.userData.info = info;
  });

  const api = {
    group,
    bands,
    beam,
    sparks,
    update(t, dt) {
      bands.forEach((b) => {
        b.tex.offset.x -= b.spec.speed * dt * 6;
      });
      // 上升光粒
      const arr = sparkGeo.attributes.position.array;
      for (let i = 0; i < sparkCount; i++) {
        const life = (t * sparkSpeed[i] * 0.34 + i * 0.137) % 1;
        const y = 0.72 + life * 3.1;
        const a = i * 2.399963; // 黄金角，避免堆积
        const r = 1.5 - life * 0.85;
        arr[i * 3] = Math.cos(a) * r + Math.sin(t * 0.6 + i) * 0.06;
        arr[i * 3 + 1] = y;
        arr[i * 3 + 2] = Math.sin(a) * r + Math.cos(t * 0.6 + i) * 0.06;
      }
      sparkGeo.attributes.position.needsUpdate = true;
      beam.material.opacity = 0.08 + Math.sin(t * 1.2) * 0.025;
    },
  };

  return api;
}
