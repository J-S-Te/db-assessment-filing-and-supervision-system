import * as THREE from 'three-signin';

/**
 * 场景 HUD：把 3D 锚点投影到屏幕坐标，再用 HTML 绘制标签。
 * 用 HTML 而不是 Sprite 文字，是为了让中文在任何分辨率下都保持清晰。
 */

const ANCHORS = [
  { id: 'session', text: 'SESSION · HttpOnly', short: 'SESSION', pos: [1.95, 1.35, 1.35] },
  { id: 'base', text: 'SUPERVISION HUB', short: 'HUB', pos: [-2.15, 1.05, 1.6], warn: true },
];

/**
 * 用途 创建随镜头移动的场景标签。
 * 参数 layerEl 为标签容器，camera 为用于投影的相机。
 * 返回值 更新与清理标签内容的方法。
 */
export function createHud(layerEl, camera) {
  const items = ANCHORS.map((a) => {
    const el = document.createElement('div');
    el.className = `hud-tag${a.warn ? ' hud-tag--warn' : ''}`;
    el.textContent = a.text;
    layerEl.appendChild(el);
    return {
      el,
      full: a.text,
      short: a.short || a.text,
      world: new THREE.Vector3(a.pos[0], a.pos[1], a.pos[2]),
    };
  });

  const v = new THREE.Vector3();

  return {
    // 用途 按已测量的画布尺寸投影标签。
    // 参数 width、height、safeBottom 和 narrow 描述当前可用显示范围。
    // 返回值 无。
    update({ width: w, height: h, safeBottom, narrow }) {
      for (const item of items) {
        v.copy(item.world).project(camera);
        const behind = v.z > 1;
        if (behind) {
          if (item.el.style.opacity !== '0') item.el.style.opacity = '0';
          continue;
        }
        let x = (v.x * 0.5 + 0.5) * w;
        const y = (-v.y * 0.5 + 0.5) * h;
        // 窄屏时场景被压缩，标签往中间收一点，避免贴边被切
        if (narrow) {
          x = w * 0.5 + (x - w * 0.5) * 0.5;
          x = Math.min(Math.max(x, 14), w - 14);
        }
        const text = narrow ? item.short : item.full;
        if (item.el.textContent !== text) item.el.textContent = text;
        const inView =
          x > -60 && x < w + 60 && y > -40 && y < h + 40 && y < safeBottom - 6;
        const opacity = inView ? '1' : '0';
        if (item.el.style.opacity !== opacity) item.el.style.opacity = opacity;
        const transform = `translate(${x}px, ${y}px)`;
        if (item.el.style.transform !== transform) item.el.style.transform = transform;
      }
    },
    dispose() {
      items.forEach((i) => i.el.remove());
    },
  };
}
