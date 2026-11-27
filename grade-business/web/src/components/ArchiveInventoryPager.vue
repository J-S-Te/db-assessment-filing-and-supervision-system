<script setup lang="ts">
defineProps<{ previous: boolean; next: boolean; loading: boolean }>()
const emit = defineEmits<{ previous: []; next: [] }>()
</script>

<template>
  <footer v-if="previous || next" class="panel-footer pager">
    <div>
      <button class="button neutral small" type="button" :disabled="loading || !previous" @click="emit('previous')">上一页</button>
      <button class="button neutral small" type="button" :disabled="loading || !next" @click="emit('next')">下一页</button>
    </div>
  </footer>
</template>
