import { createStage } from './scene/index.js'
import { createHud } from './ui/hud.js'
import { createFuiOverlay } from './ui/fui.js'

/**
 * 用途 将原型场景与登录页挂载元素关联，并同步可见性和渲染输出。
 * 参数 elements 为画布和标签层，signal 为页面退出信号，onResize、onLost 为状态回调。
 * 返回值 可切换画质和释放的场景控制器。
 */
export async function mountScene({ canvas, layer }, signal, onResize, onLost) {
  const stage = await createStage(canvas, { signal })
  if (signal.aborted) {
    stage.dispose()
    signal.throwIfAborted()
  }

  const card = canvas.closest('.signin-3d')?.querySelector('.login-card')
  const motion = window.matchMedia('(prefers-reduced-motion: reduce)')
  const narrowLayout = window.matchMedia('(max-width: 900px), (max-height: 620px)')
  const bounds = { width: 0, height: 0, safeBottom: 0, narrow: false }
  const measureLayout = () => {
    bounds.width = canvas.clientWidth
    bounds.height = canvas.clientHeight
    bounds.narrow = narrowLayout.matches
    bounds.safeBottom = bounds.narrow && card
      ? Math.max(0, card.getBoundingClientRect().top - canvas.getBoundingClientRect().top)
      : bounds.height
  }
  const hud = createHud(layer, stage.camera)
  const overlay = createFuiOverlay(layer, stage.camera, stage.fui)
  const cardObserver = new ResizeObserver(measureLayout)
  if (card) cardObserver.observe(card)
  const syncVisibility = () => document.hidden ? stage.stop() : stage.start()
  const syncMotion = () => { stage.reducedMotion = motion.matches }
  const reportSize = () => {
    measureLayout()
    onResize(stage.getResolution(), canvas.width, canvas.height)
  }
  const loseContext = () => {
    dispose()
    onLost()
  }

  stage.onSelect = (_info, point, object) => stage.focus(point, object)
  stage.onResize = reportSize
  stage.onFrame = () => {
    hud.update(bounds)
    overlay.update(bounds)
  }
  let disposed = false

  /**
   * 用途 停止渲染并移除随登录页挂载的监听与叠层。
   * 参数 无。
   * 返回值 无。
   */
  function dispose() {
    if (disposed) return
    disposed = true
    canvas.removeEventListener('webglcontextlost', loseContext)
    document.removeEventListener('visibilitychange', syncVisibility)
    motion.removeEventListener('change', syncMotion)
    narrowLayout.removeEventListener('change', measureLayout)
    cardObserver.disconnect()
    hud.dispose()
    overlay.dispose()
    stage.dispose()
  }

  canvas.addEventListener('webglcontextlost', loseContext)
  document.addEventListener('visibilitychange', syncVisibility)
  motion.addEventListener('change', syncMotion)
  narrowLayout.addEventListener('change', measureLayout)
  syncMotion()
  reportSize()
  syncVisibility()
  return { setResolution: scale => stage.setResolution(scale), dispose }
}
