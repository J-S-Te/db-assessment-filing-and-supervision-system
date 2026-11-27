<script setup lang="ts">
import { onMounted, ref } from 'vue'

defineProps<{ label: string; width: number }>()
defineEmits<{ close: [] }>()
const dialog = ref<HTMLDialogElement>()

/**
 * 用途 挂载后在浏览器顶层打开业务面板，以便键盘操作只进入当前面板。
 * 参数 无。
 * 返回值 无。
 */
function open() {
  if (dialog.value && !dialog.value.open) dialog.value.showModal()
}

onMounted(open)
</script>

<template>
  <dialog ref="dialog" class="v2-route-dialog" :style="{ '--dialog-width': width + 'px' }" :aria-label="label"
    @cancel.self.prevent="$emit('close')">
    <button type="button" class="v2-route-close" aria-label="关闭业务窗口" @click="$emit('close')">×</button>
    <slot />
  </dialog>
</template>
