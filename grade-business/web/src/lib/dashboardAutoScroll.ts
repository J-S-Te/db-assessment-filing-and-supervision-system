import type { Directive } from 'vue'

const pixelsPerMillisecond = .024
const disposeByElement = new WeakMap<HTMLElement, () => void>()

/**
 * 用途 复制一轮可见记录，让第一条紧接最后一条并避免重复进入键盘顺序。
 * 参数 original 为 Vue 渲染的记录组，copy 为纯展示的尾部接续组。
 * 返回值 无。
 */
function fillLoopCopy(original: HTMLElement, copy: HTMLElement) {
  const copies = Array.from(original.children, item => {
    const row = item.cloneNode(true) as Element
    row.removeAttribute('id')
    for (const child of row.querySelectorAll('[id]')) child.removeAttribute('id')
    if (row.matches('a[href]')) row.setAttribute('tabindex', '-1')
    for (const link of row.querySelectorAll('a[href]')) link.setAttribute('tabindex', '-1')
    return row
  })
  copy.replaceChildren(...copies)
}

/**
 * 用途 判断原始记录和固定表头是否超出当前可视高度。
 * 参数 element 为滚动视口，original 为原始记录组。
 * 返回值 原始内容超出视口时为真。
 */
function hasLoopOverflow(element: HTMLElement, original: HTMLElement) {
  const edge = original.getBoundingClientRect().bottom - element.getBoundingClientRect().top
  const padding = Number.parseFloat(getComputedStyle(element).paddingBottom) || 0
  const height = edge + element.scrollTop - element.clientTop + padding
  return element.clientHeight > 0 && height > element.clientHeight + 1
}

/**
 * 用途 为看板列表安装原生滚动、首尾接续和交互暂停。
 * 参数 element 为滚动视口，original 为原始记录组，copy 为首尾接续组。
 * 返回值 取消播放及观察、监听的清理函数。
 */
function installScroll(element: HTMLElement, original: HTMLElement, copy: HTMLElement) {
  const controller = new AbortController()
  const motion = window.matchMedia('(prefers-reduced-motion: reduce)')
  const options = { signal: controller.signal }
  element.classList.add('dashboard-loop-list')
  let hovered = window.matchMedia('(any-hover: hover)').matches && element.matches(':hover')
  let touching = false
  let touchTimer = 0
  let frame = 0
  let lastTime = 0
  let carry = 0
  let period = 0

  /**
   * 用途 根据滚动范围及交互状态开始或暂停播放。
   * 参数 无，读取当前列表和浏览器状态。
   * 返回值 无。
   */
  function sync() {
    if (controller.signal.aborted) return
    const paused = hovered || touching || element.matches(':focus-within') || document.hidden || motion.matches
    if (period > 0 && !paused) {
      if (!frame) frame = requestAnimationFrame(tick)
      return
    }
    cancelAnimationFrame(frame)
    frame = lastTime = carry = 0
  }

  /**
   * 用途 按帧推进列表，在两组记录视觉位置相同时折算下一轮偏移。
   * 参数 time 为浏览器动画帧时间戳。
   * 返回值 无。
   */
  function tick(time: number) {
    frame = 0
    const elapsed = lastTime ? Math.min(time - lastTime, 50) : 0
    lastTime = time
    const distance = carry + elapsed * pixelsPerMillisecond
    const pixels = Math.floor(distance)
    carry = distance - pixels
    if (period > 0 && pixels) element.scrollTop = (element.scrollTop + pixels) % period
    sync()
  }

  /**
   * 用途 随视口和业务数据更新播放距离，仅在原始记录溢出时生成接续组。
   * 参数 refreshCopy 表示原始记录的文字、链接或样式已更新。
   * 返回值 无。
   */
  function measure(refreshCopy = false) {
    if (controller.signal.aborted) return
    if (!motion.matches && hasLoopOverflow(element, original)) {
      if (copy.hidden || refreshCopy) fillLoopCopy(original, copy)
      copy.hidden = false
      period = Math.round(copy.getBoundingClientRect().top - original.getBoundingClientRect().top)
    } else if (!copy.hidden) {
      const position = period > 0 ? element.scrollTop % period : element.scrollTop
      copy.hidden = true
      copy.replaceChildren()
      element.scrollTop = position
      period = 0
    }
    sync()
  }

  const resize = new ResizeObserver(() => measure())
  resize.observe(element)
  resize.observe(original)
  const changes = new MutationObserver(() => measure(true))
  changes.observe(original, { childList: true, subtree: true, characterData: true, attributes: true })
  copy.addEventListener('click', event => {
    if (event.button || event.metaKey || event.ctrlKey || event.altKey || event.shiftKey) return
    const link = event.target instanceof Element ? event.target.closest<HTMLAnchorElement>('a[href]') : null
    if (!link) return
    const sourceLink = Array.from(original.querySelectorAll<HTMLAnchorElement>('a[href]'))
      .find(item => item.href === link.href)
    if (sourceLink) {
      event.preventDefault()
      sourceLink.click()
    }
  }, options)
  element.addEventListener('pointerenter', event => {
    if (event.pointerType !== 'touch') hovered = true
    sync()
  }, options)
  element.addEventListener('pointerleave', event => {
    if (event.pointerType !== 'touch') hovered = false
    sync()
  }, options)
  element.addEventListener('focusin', sync, options)
  element.addEventListener('focusout', () => queueMicrotask(sync), options)
  element.addEventListener('touchstart', () => {
    window.clearTimeout(touchTimer)
    touching = true
    sync()
  }, { ...options, passive: true })
  for (const event of ['touchend', 'touchcancel'] as const) {
    element.addEventListener(event, () => {
      window.clearTimeout(touchTimer)
      touchTimer = window.setTimeout(() => { touching = false; sync() }, 1_000)
    }, { ...options, passive: true })
  }
  motion.addEventListener('change', () => measure(), options)
  document.addEventListener('visibilitychange', sync, options)
  measure()
  return () => {
    controller.abort()
    changes.disconnect()
    resize.disconnect()
    window.clearTimeout(touchTimer)
    cancelAnimationFrame(frame)
  }
}

export const vDashboardScroll: Directive<HTMLElement> = {
  mounted: element => {
    const original = element.querySelector<HTMLElement>('[data-dashboard-loop-original]')
    const copy = element.querySelector<HTMLElement>('[data-dashboard-loop-copy]')
    if (original && copy) disposeByElement.set(element, installScroll(element, original, copy))
  },
  beforeUnmount: element => {
    disposeByElement.get(element)?.()
    disposeByElement.delete(element)
  },
}
