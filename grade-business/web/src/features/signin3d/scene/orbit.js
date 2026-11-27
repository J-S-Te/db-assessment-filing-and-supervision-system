import * as THREE from 'three-signin';
import { createDotSprite } from './textures.js';

/**
 * 轨道环系统：三条不同半径/倾角的虚线轨道 + 沿轨道流动的粒子
 * 轨道粒子是场景里最明显的「持续运行」信号
 */
export function createOrbit() {
  const group = new THREE.Group();
  group.name = 'orbitGroup';

  const sprite = createDotSprite('rgba(190,240,255,1)', 'rgba(90,190,255,0)');

  const specs = [
    { r: 3.35, y: 1.55, tilt: 0.2, spin: 0.17, color: 0x3fa9e0, opacity: 0.55, count: 54, dashed: true },
    { r: 4.25, y: 3.05, tilt: -0.36, spin: -0.12, color: 0x2b7fc0, opacity: 0.36, count: 46, dashed: true },
    { r: 5.15, y: 0.95, tilt: 0.55, spin: 0.085, color: 0x1d5c95, opacity: 0.2, count: 38, dashed: false },
  ];

  const tracks = [];

  specs.forEach((spec, idx) => {
    const holder = new THREE.Group();
    holder.position.y = spec.y;
    holder.rotation.x = spec.tilt;
    group.add(holder);

    // 轨道线
    const pts = [];
    const SEG = 240;
    for (let i = 0; i <= SEG; i++) {
      const a = (i / SEG) * Math.PI * 2;
      pts.push(new THREE.Vector3(Math.cos(a) * spec.r, 0, Math.sin(a) * spec.r));
    }
    const lineGeo = new THREE.BufferGeometry().setFromPoints(pts);
    const lineMat = spec.dashed
      ? new THREE.LineDashedMaterial({
          color: spec.color,
          transparent: true,
          opacity: spec.opacity,
          dashSize: 0.12,
          gapSize: 0.11,
        })
      : new THREE.LineBasicMaterial({
          color: spec.color,
          transparent: true,
          opacity: spec.opacity,
        });
    const line = new THREE.Line(lineGeo, lineMat);
    line.name = `orbitLine${idx}`;
    if (spec.dashed) line.computeLineDistances();
    holder.add(line);

    // 流动粒子
    const count = spec.count;
    const positions = new Float32Array(count * 3);
    const phases = new Float32Array(count);
    const speeds = new Float32Array(count);
    const sizes = new Float32Array(count);
    for (let i = 0; i < count; i++) {
      phases[i] = (i / count) * Math.PI * 2 + Math.random() * 0.4;
      speeds[i] = 0.7 + Math.random() * 0.9;
      sizes[i] = 0.045 + Math.random() * 0.075;
      positions[i * 3] = Math.cos(phases[i]) * spec.r;
      positions[i * 3 + 1] = 0;
      positions[i * 3 + 2] = Math.sin(phases[i]) * spec.r;
    }
    const geo = new THREE.BufferGeometry();
    geo.setAttribute('position', new THREE.BufferAttribute(positions, 3));
    geo.setAttribute('size', new THREE.BufferAttribute(sizes, 1));
    const mat = new THREE.PointsMaterial({
      color: 0xa9ecff,
      map: sprite,
      size: 0.16,
      sizeAttenuation: true,
      transparent: true,
      opacity: 0.95,
      depthWrite: false,
      blending: THREE.AdditiveBlending,
    });
    const points = new THREE.Points(geo, mat);
    points.name = `orbitParticles${idx}`;
    holder.add(points);

    tracks.push({ holder, spec, positions, phases, speeds, geo });
  });

  const info = {
    id: 'orbit',
    name: '数据轨道环 · Data Orbit',
    eyebrow: 'NETWORK / 数据链路',
    desc:
      '三条倾斜的轨道环代表平台与各业务系统之间的实时数据链路。' +
      '环上的光点以不同速度沿轨道流动，密度越高表示该链路上的请求越密集。' +
      '轨道可以自由环绕观察，但速度是固定的——它反映的是系统吞吐，而不是装饰动画。',
    specs: [
      ['轨道数', '3 条（不同倾角）'],
      ['半径', '3.35 / 4.25 / 5.15'],
      ['粒子', '138 个流动光点'],
      ['方向', '内外反向旋转'],
    ],
  };

  tracks.forEach((tr) => {
    tr.holder.children.forEach((child) => {
      child.userData.info = info;
    });
  });

  const api = {
    group,
    tracks,
    update(t, dt) {
      tracks.forEach((tr) => {
        tr.holder.rotation.y += tr.spec.spin * dt;
        const arr = tr.positions;
        const n = tr.phases.length;
        for (let i = 0; i < n; i++) {
          const a = tr.phases[i] + t * tr.spec.spin * 1.6 * tr.speeds[i];
          arr[i * 3] = Math.cos(a) * tr.spec.r;
          arr[i * 3 + 1] = Math.sin(a * 3 + t * 0.7) * 0.06;
          arr[i * 3 + 2] = Math.sin(a) * tr.spec.r;
        }
        tr.geo.attributes.position.needsUpdate = true;
      });
    },
  };

  return api;
}
