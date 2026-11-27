import * as THREE from 'three-signin';

/** 盾形轮廓：平顶 + 圆角肩 + 收到底部的锐角 */
function buildShieldShape(halfWidth = 1.06, top = 1.32, bottom = -1.36) {
  const s = new THREE.Shape();
  const corner = halfWidth * 0.16;
  const shoulder = top - 0.26;

  s.moveTo(-halfWidth, shoulder);
  s.quadraticCurveTo(-halfWidth, top, -halfWidth + corner, top);
  s.lineTo(halfWidth - corner, top);
  s.quadraticCurveTo(halfWidth, top, halfWidth, shoulder);
  s.bezierCurveTo(halfWidth, top - 0.78, halfWidth * 0.78, bottom + 0.2, 0, bottom);
  s.bezierCurveTo(-halfWidth * 0.78, bottom + 0.2, -halfWidth, top - 0.78, -halfWidth, shoulder);
  s.closePath();
  return s;
}

/** 由轮廓点算出包围盒 */
function boundsOf(pts) {
  let minX = Infinity;
  let maxX = -Infinity;
  let minY = Infinity;
  let maxY = -Infinity;
  pts.forEach((p) => {
    if (p.x < minX) minX = p.x;
    if (p.x > maxX) maxX = p.x;
    if (p.y < minY) minY = p.y;
    if (p.y > maxY) maxY = p.y;
  });
  return { minX, maxX, minY, maxY };
}

/**
 * 用途 生成模型和贴图共同使用的盾牌轮廓及包围盒。
 * 参数 size 为盾牌贴图的边长，缺省使用原始尺寸。
 * 返回值 盾形、采样点、边界和贴图坐标参数。
 */
export function createShieldProfile(size = 2048) {
  const shape = buildShieldShape();
  const outline = shape.getPoints(220);
  const bbox = boundsOf(outline);
  const pbrOptions = {
    size,
    cx: (bbox.minX + bbox.maxX) / 2,
    cy: (bbox.minY + bbox.maxY) / 2,
    spanX: bbox.maxX - bbox.minX,
    spanY: bbox.maxY - bbox.minY,
  };
  return { shape, outline, bbox, pbrOptions };
}
