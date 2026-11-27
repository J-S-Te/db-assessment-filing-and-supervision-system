<script setup lang="ts">
import { useRoute } from 'vue-router'
import type { ArchiveCursor } from '../lib/archives'
import { queryValue } from '../lib/paging'

defineProps<{ field: ArchiveCursor; nextCursor?: string }>()
const route = useRoute()
</script>

<template>
  <div v-if="queryValue(route.query[field]) || nextCursor" class="panel-footer">
    <RouterLink v-if="queryValue(route.query[field])" class="button neutral small"
      :to="{ query: { ...route.query, [field]: undefined } }">返回第一页</RouterLink>
    <RouterLink v-if="nextCursor" class="button neutral small"
      :to="{ query: { ...route.query, [field]: nextCursor } }">下一页</RouterLink>
  </div>
</template>
