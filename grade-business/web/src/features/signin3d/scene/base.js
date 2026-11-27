import * as THREE from 'three-signin';
import { createRadialDeckPBR, createSidePanelPBR } from './pbr.js';

/**
 * 八边形防护基座：舱室地台
 *
 * 真实感来自四件事：
 *   1. 台面用「同心拉丝金属」PBR 贴图，环槽是真正的凹槽（法线贴图里有高度）
 *   2. 环槽里嵌红色自发光，只有槽底在发红光，不是整个盘面泛红
 *   3. 台面与侧壁之间有一圈真实倒角，侧壁有散热槽
 *   4. 八颗六角螺栓压在倒角上
 */
/**
 * 用途 装配基座模型及台面和侧板的原尺寸贴图。
 * 参数 opts 为贴图尺寸和可选的台面、侧板预生成贴图。
 * 返回值 可更新场景动画的基座。
 */
export function createBase(opts = {}) {
  const group = new THREE.Group();
  group.name = 'baseGroup';

  const R = 2.62;
  const TEX = opts.textureSize || 2048;

  // ---------------- PBR 贴图 ----------------
  const deckPBR = opts.deckPbr ?? createRadialDeckPBR({ size: TEX });
  const sidePBR = opts.sidePbr ?? createSidePanelPBR({ size: Math.min(TEX, 1024) });

  const deckMaterial = new THREE.MeshPhysicalMaterial({
    map: deckPBR.map,
    normalMap: deckPBR.normalMap,
    roughnessMap: deckPBR.roughnessMap,
    metalnessMap: deckPBR.metalnessMap,
    aoMap: deckPBR.aoMap,
    aoMapIntensity: 1,
    emissiveMap: deckPBR.emissiveMap,
    emissive: 0xffffff,
    emissiveIntensity: 0.85,
    normalScale: new THREE.Vector2(1.5, 1.5),
    color: 0xffffff,
    metalness: 1,
    roughness: 1,
    clearcoat: 0.35,
    clearcoatRoughness: 0.3,
    envMapIntensity: 1.0,
  });

  const sideMaterial = new THREE.MeshPhysicalMaterial({
    map: sidePBR.map,
    normalMap: sidePBR.normalMap,
    roughnessMap: sidePBR.roughnessMap,
    metalnessMap: sidePBR.metalnessMap,
    aoMap: sidePBR.aoMap,
    aoMapIntensity: 0.9,
    normalScale: new THREE.Vector2(1.35, 1.35),
    color: 0xffffff,
    metalness: 1,
    roughness: 1,
    clearcoat: 0.15,
    envMapIntensity: 0.55,
  });

  // ---------------- 下层台阶 ----------------
  const step = new THREE.Mesh(
    new THREE.CylinderGeometry(R * 1.16, R * 1.26, 0.3, 8, 1),
    sideMaterial
  );
  step.name = 'baseStep';
  step.position.y = 0.15;
  step.rotation.y = Math.PI / 8;
  step.receiveShadow = true;
  group.add(step);

  // ---------------- 台体侧壁 ----------------
  const wall = new THREE.Mesh(
    new THREE.CylinderGeometry(R * 0.97, R, 0.62, 8, 1, true),
    sideMaterial
  );
  wall.name = 'baseWall';
  wall.position.y = 0.36;
  wall.rotation.y = Math.PI / 8;
  wall.receiveShadow = true;
  group.add(wall);

  // ---------------- 顶部倒角 ----------------
  const chamfer = new THREE.Mesh(
    new THREE.CylinderGeometry(R * 0.9, R * 0.97, 0.09, 8, 1, true),
    new THREE.MeshPhysicalMaterial({
      color: 0x6d7f95,
      metalness: 1,
      roughness: 0.4,
      clearcoat: 0.35,
      clearcoatRoughness: 0.22,
      envMapIntensity: 1.0,
      side: THREE.DoubleSide,
    })
  );
  chamfer.name = 'baseChamfer';
  chamfer.position.y = 0.705;
  chamfer.rotation.y = Math.PI / 8;
  group.add(chamfer);

  // ---------------- 台面 ----------------
  const top = new THREE.Mesh(new THREE.CircleGeometry(R * 0.9, 8), deckMaterial);
  top.name = 'baseTop';
  top.rotation.x = -Math.PI / 2;
  top.rotation.z = Math.PI / 8;
  top.position.y = 0.75;
  top.receiveShadow = true;
  group.add(top);

  // ---------------- 八角螺栓 ----------------
  const boltMaterial = new THREE.MeshPhysicalMaterial({
    color: 0x8ea3b8,
    metalness: 1,
    roughness: 0.38,
    clearcoat: 0.3,
    envMapIntensity: 1.0,
  });
  const boltGeo = new THREE.CylinderGeometry(0.075, 0.085, 0.06, 6, 1);
  const bolts = new THREE.Group();
  bolts.name = 'baseBolts';
  for (let i = 0; i < 8; i++) {
    const a = (i / 8) * Math.PI * 2 + Math.PI / 8;
    const bolt = new THREE.Mesh(boltGeo, boltMaterial);
    bolt.position.set(Math.cos(a) * R * 0.935, 0.735, Math.sin(a) * R * 0.935);
    bolt.rotation.y = a;
    bolt.castShadow = true;
    bolts.add(bolt);
  }
  group.add(bolts);

  // ---------------- 红色发光外环 ----------------
  const glowRing = new THREE.Mesh(
    new THREE.RingGeometry(R * 0.9, R * 1.02, 8, 1),
    new THREE.MeshBasicMaterial({
      color: 0xff3b52,
      transparent: true,
      opacity: 0.42,
      side: THREE.DoubleSide,
      blending: THREE.AdditiveBlending,
      depthWrite: false,
    })
  );
  glowRing.name = 'baseGlowRing';
  glowRing.rotation.x = -Math.PI / 2;
  glowRing.rotation.z = Math.PI / 8;
  glowRing.position.y = 0.755;
  group.add(glowRing);

  // 悬浮在基座上方的一圈淡红光晕
  const aura = new THREE.Mesh(
    new THREE.RingGeometry(R * 0.74, R * 1.12, 64, 1),
    new THREE.MeshBasicMaterial({
      color: 0xff2f48,
      transparent: true,
      opacity: 0.05,
      side: THREE.DoubleSide,
      blending: THREE.AdditiveBlending,
      depthWrite: false,
    })
  );
  aura.name = 'baseAura';
  aura.rotation.x = -Math.PI / 2;
  aura.position.y = 0.76;
  group.add(aura);

  // ---------------- 棱线灯条 ----------------
  const edgeMat = new THREE.LineBasicMaterial({
    color: 0xff6a7d,
    transparent: true,
    opacity: 0.55,
  });
  const edgeGeo = new THREE.EdgesGeometry(
    new THREE.CylinderGeometry(R * 0.97, R, 0.62, 8, 1)
  );
  const edges = new THREE.LineSegments(edgeGeo, edgeMat);
  edges.name = 'baseEdges';
  edges.position.y = 0.36;
  edges.rotation.y = Math.PI / 8;
  group.add(edges);

  // ---------------- 中央六棱发射台 ----------------
  // 这块「盖子」属于基座，不跟着盾牌浮动；
  // 盾牌悬浮在它上方，中间始终留一道缝
  const pedestal = new THREE.Mesh(
    new THREE.CylinderGeometry(0.62, 0.88, 0.26, 6, 1),
    new THREE.MeshPhysicalMaterial({
      color: 0x1a2b40,
      metalness: 1,
      roughness: 0.34,
      clearcoat: 0.3,
      envMapIntensity: 1.5,
    })
  );
  pedestal.name = 'shieldPedestal';
  pedestal.position.y = 0.82;
  pedestal.castShadow = true;
  pedestal.receiveShadow = true;
  group.add(pedestal);

  // 发射台顶面的发光圈
  const emitRing = new THREE.Mesh(
    new THREE.TorusGeometry(0.66, 0.022, 10, 6),
    new THREE.MeshBasicMaterial({
      color: 0x46d8ff,
      transparent: true,
      opacity: 0.55,
      blending: THREE.AdditiveBlending,
      depthWrite: false,
    })
  );
  emitRing.name = 'baseEmitRing';
  emitRing.rotation.x = Math.PI / 2;
  emitRing.position.y = 0.96;
  group.add(emitRing);

  // ---------------- 底座灯光 ----------------
  const light = new THREE.PointLight(0xff3b52, 1.5, 13, 2);
  light.position.set(0, 0.95, 0);
  group.add(light);

  const under = new THREE.PointLight(0xff2f48, 0.5, 8, 2);
  under.position.set(0, -0.5, 0);
  group.add(under);

  const info = {
    id: 'base',
    name: '八边形监管中枢基座 · Supervision Hub',
    eyebrow: 'FOUNDATION / 监管中枢',
    desc:
      '基座是舱室的地台，也是监管中枢的物理承载与红色警戒锚点。台面由同心拉丝金属板铺成，' +
      '每一道环槽都是真实凹下去的凹槽，红光只从槽底透出来，而不是整块盘面泛红；' +
      '台面与侧壁之间有一圈金属倒角，八颗六角螺栓压在上面。八边形对应传统安防八角徽记，' +
      '外扩的扫描环表示监管体系正在逐层向外覆盖全市。',
    specs: [
      ['形态', '正八边形 · 双层台体'],
      ['台面', '同心拉丝金属 · 9 道环槽'],
      ['发光', '槽底红色内嵌光'],
      ['细节', '8 × 六角螺栓 + 侧壁散热槽'],
    ],
  };

  [step, wall, chamfer, top, glowRing, edges, pedestal, emitRing].forEach(
    (o) => (o.userData.info = info)
  );
  bolts.children.forEach((b) => (b.userData.info = info));

  const api = {
    group,
    body: wall,
    top,
    chamfer,
    glowRing,
    aura,
    edges,
    pedestal,
    emitRing,
    light,
    update(t) {
      // 告警呼吸：红光明暗起伏
      const pulse = 0.36 + Math.sin(t * 1.5) * 0.09 + Math.sin(t * 4.1) * 0.025;
      glowRing.material.opacity = pulse;
      aura.material.opacity = 0.02 + Math.sin(t * 1.5) * 0.009;
      light.intensity = 1.45 + Math.sin(t * 1.5) * 0.45;
      deckMaterial.emissiveIntensity = 0.72 + Math.sin(t * 1.5) * 0.22;
      // 刻度环缓慢旋转
      top.rotation.z = Math.PI / 8 + t * 0.035;
      edges.rotation.y = Math.PI / 8 + t * 0.035;
      bolts.rotation.y = t * 0.035;
      emitRing.rotation.z = t * 0.55;
      emitRing.material.opacity = 0.42 + Math.sin(t * 1.6) * 0.12;
    },
  };

  return api;
}
