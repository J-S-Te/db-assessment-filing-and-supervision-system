<script setup lang="ts">
import { statuses, type Status } from '../lib/domain'

defineProps<{ status: Status; label?: string }>()
</script>

<template>
  <span class="status-badge" :class="status === 'withdrawn' ? 'status-draft' : 'status-' + status">
    <span aria-hidden="true"></span>{{ label ?? statuses[status] ?? status }}
  </span>
</template>
