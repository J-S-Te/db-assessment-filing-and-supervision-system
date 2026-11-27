import * as THREE from 'three-signin';

/**
 * FUI 文字叠层
 *
 * 三维面板负责线条、雷达、条形这些「图形」，
 * 中文读数交给 DOM，用投影定位到 3D 面板上——保证任何缩放下都清晰。
 */

/**
 * 用途 创建随镜头投影的全息仪表文字。
 * 参数 layerEl 为叠层容器，camera 为投影相机，fui 提供文字锚点。
 * 返回值 投影更新与叠层清理方法。
 */
export function createFuiOverlay(layerEl, camera, fui) {
  const root = document.createElement('div');
  root.className = 'fui-layer';
  layerEl.appendChild(root);

  const items = [];
  const v = new THREE.Vector3();

  fui.resolveAnchors().forEach((a) => {
    const el = document.createElement('div');
    el.className = `fui-text fui-text--${a.kind}`;
    el.textContent = a.text;
    if (a.offsetY) el.style.marginTop = `${a.offsetY}px`;
    root.appendChild(el);
    const originX = a.align === 'left' ? '0%' : a.align === 'right' ? '-100%' : '-50%';
    items.push({ el, anchor: a, originX });
  });

  /**
   * 用途 用缓存的显示范围投影全息仪表文字。
   * 参数 width、height、safeBottom 和 narrow 描述当前可用显示范围。
   * 返回值 无。
   */
  function update({ width: w, height: h, safeBottom, narrow }) {
    const display = narrow ? 'none' : '';
    if (root.style.display !== display) root.style.display = display;
    if (narrow) return;

    items.forEach((item) => {
      v.copy(item.anchor.world).project(camera);
      const behind = v.z > 1;
      const x = (v.x * 0.5 + 0.5) * w;
      const y = (-v.y * 0.5 + 0.5) * h;
      const inView = x > -120 && x < w + 120 && y > -60 && y < h + 60 && y < safeBottom - 4;

      const opacity = behind || !inView ? '0' : '1';
      if (item.el.style.opacity !== opacity) item.el.style.opacity = opacity;
      const transform = `translate(${item.originX}, -50%) translate(${x}px, ${y}px)`;
      if (item.el.style.transform !== transform) item.el.style.transform = transform;
    });
  }

  return {
    root,
    update,
    dispose() {
      root.remove();
    },
  };
}
