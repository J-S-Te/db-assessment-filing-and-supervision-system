<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'

const clockText = ref('')
let desktop: MediaQueryList | undefined
let timer = 0

/**
 * 用途 更新登录页指挥条显示的本地日期和时间。
 * 参数 无。
 * 返回值 无。
 */
function updateClock() {
  const now = new Date()
  const [month, day, hour, minute, second] = [
    now.getMonth() + 1, now.getDate(), now.getHours(), now.getMinutes(), now.getSeconds(),
  ].map(value => String(value).padStart(2, '0'))
  clockText.value = `${now.getFullYear()}-${month}-${day} ${hour}:${minute}:${second}`
}

/**
 * 用途 仅在时钟可见时每秒刷新，重新显示时立即补齐当前时间。
 * 参数 无。
 * 返回值 无。
 */
function syncClock() {
  window.clearInterval(timer)
  if (!desktop?.matches || document.hidden) return
  updateClock()
  timer = window.setInterval(updateClock, 1000)
}

onMounted(() => {
  desktop = window.matchMedia('(width > 760px)')
  desktop.addEventListener('change', syncClock)
  document.addEventListener('visibilitychange', syncClock)
  syncClock()
})

onUnmounted(() => {
  window.clearInterval(timer)
  desktop?.removeEventListener('change', syncClock)
  document.removeEventListener('visibilitychange', syncClock)
})
</script>

<template>
  <time>{{ clockText }}</time>
</template>
