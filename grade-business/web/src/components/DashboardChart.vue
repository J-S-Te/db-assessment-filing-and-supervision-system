<script setup lang="ts">
import { onMounted, onScopeDispose, ref, watch } from 'vue'
import { init, use, type ComposeOption, type EChartsType } from 'echarts/core'
import { LineChart, PieChart, type LineSeriesOption, type PieSeriesOption } from 'echarts/charts'
import { GridComponent, TooltipComponent, type GridComponentOption, type TooltipComponentOption } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'

use([LineChart, PieChart, GridComponent, TooltipComponent, CanvasRenderer])

type DashboardOption = ComposeOption<LineSeriesOption | PieSeriesOption | GridComponentOption | TooltipComponentOption>
const props = defineProps<{ option: DashboardOption; highlight?: number }>()
const emit = defineEmits<{
  point: [index: number]; pointClick: [index: number]; sliceHover: [name: string]; sliceClick: [name: string]
}>()
const canvas = ref<HTMLDivElement>()
let instance: EChartsType | undefined
let observer: ResizeObserver | undefined
let reduceMotion: MediaQueryList | undefined

/**
 * 用途 让键盘或图例选中的环图扇区与图表高亮同步。
 * 参数 无，highlight 传入非空扇区在当前序列中的位置。
 * 返回值 无。
 */
function highlightSlice() {
  if (!instance || props.highlight === undefined) return
  instance.dispatchAction({ type: 'downplay', seriesIndex: 0 })
  if (props.highlight >= 0) instance.dispatchAction({ type: 'highlight', seriesIndex: 0, dataIndex: props.highlight })
}

/**
 * 用途 应用业务图表配置，并根据系统偏好控制短过渡。
 * 参数 无，使用当前数据及浏览器偏好。
 * 返回值 无。
 */
function render() {
  instance?.setOption({
    ...props.option, animation: reduceMotion?.matches === false,
    animationDuration: 360, animationDurationUpdate: 220,
  }, { replaceMerge: ['series'] })
  highlightSlice()
}

/**
 * 用途 在有可用尺寸时挂载图表并处理容器和全屏尺寸变化。
 * 参数 无，使用组件所拥有的图表容器。
 * 返回值 无。
 */
function resize() {
  const host = canvas.value
  if (!host?.clientWidth || !host.clientHeight) return
  if (instance) {
    instance.resize()
    return
  }
  instance = init(host, undefined, { renderer: 'canvas', devicePixelRatio: Math.min(window.devicePixelRatio, 2) })
  render()
  instance.on('updateAxisPointer', event => {
    const pointer = event as { axesInfo?: { value: number | string }[] }
    const index = pointer.axesInfo?.[0]?.value
    if (typeof index === 'number' && Number.isInteger(index)) emit('point', index)
  })
  instance.on('mouseover', 'series.pie', event => emit('sliceHover', event.name))
  instance.on('mouseout', 'series.pie', () => emit('sliceHover', ''))
  instance.on('globalout', () => emit('sliceHover', ''))
  instance.on('click', 'series.pie', event => emit('sliceClick', event.name))
  instance.on('click', 'series.line', event => emit('pointClick', event.dataIndex))
}

onMounted(() => {
  if (!canvas.value) return
  reduceMotion = window.matchMedia('(prefers-reduced-motion: reduce)')
  reduceMotion.addEventListener('change', render)
  observer = new ResizeObserver(resize)
  observer.observe(canvas.value)
  resize()
})

watch(() => props.option, render, { flush: 'post' })
watch(() => props.highlight, highlightSlice, { flush: 'post' })
onScopeDispose(() => {
  reduceMotion?.removeEventListener('change', render)
  observer?.disconnect()
  instance?.dispose()
})
</script>

<template><div ref="canvas" class="dashboard-chart" aria-hidden="true"></div></template>

<style scoped>
.dashboard-chart {
  width: 100%; height: 100%; min-width: 0; min-height: 0;
  /* 容器缩小时，画布在 ResizeObserver 更新前可能仍沿用旧尺寸。 */
  overflow: clip;
}
</style>
