<script setup lang="ts">
import { AlertCircle, Inbox, LoaderCircle } from '@lucide/vue'

defineProps<{ loading?: boolean; error?: string; empty?: boolean; emptyText?: string }>()
defineEmits<{ retry: [] }>()
</script>

<template>
  <div v-if="loading" class="request-state" data-state="loading" role="status">
    <LoaderCircle class="spinner" :size="25" aria-hidden="true" />加载中…
  </div>
  <div v-else-if="error" class="request-state request-error" data-state="error" role="alert">
    <AlertCircle :size="24" aria-hidden="true" />{{ error }}
    <button class="button neutral small" type="button" @click="$emit('retry')">重试</button>
  </div>
  <div v-else-if="empty" class="request-state empty-state" data-state="empty" role="status">
    <Inbox :size="28" aria-hidden="true" />{{ emptyText || '暂无记录' }}
  </div>
  <slot v-else />
</template>
