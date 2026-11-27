<script setup lang="ts">
import { computed } from 'vue'
import { Download } from '@lucide/vue'
import { attachmentURL } from '../lib/api'
import { formatSize } from '../lib/domain'
import { publicationAttachmentDownload, type PublicationAttachment, type PublicationTarget } from '../lib/publicationDelivery'

const props = defineProps<{
  id: string
  version: number
  attachments?: PublicationAttachment[]
  recipient?: PublicationTarget
  removable?: boolean
  disabled?: boolean
  compact?: boolean
}>()
defineEmits<{ remove: [item: PublicationAttachment] }>()
const target = computed(() => publicationAttachmentDownload(props.id, props.version, props.recipient))
</script>

<template>
  <section v-if="target && attachments?.length" class="publication-attachments" aria-label="公文附件">
    <h3 v-if="!compact">公文附件</h3>
    <ul>
      <li v-for="file in attachments" :key="file.id">
        <div class="publication-file"><span>{{ file.fileName }}</span><small class="muted">{{ formatSize(file.size) }}</small></div>
        <a class="text-action" :href="attachmentURL(target.recordPath, file.id) + target.query" :download="file.fileName"
          :aria-label="'下载 ' + file.fileName"><Download :size="14" aria-hidden="true" />下载</a>
        <button v-if="removable" type="button" class="text-action remove-file" :disabled="disabled"
          :aria-label="'移除 ' + file.fileName" @click="$emit('remove', file)">移除</button>
      </li>
    </ul>
  </section>
</template>

<style scoped>
.publication-attachments { min-width: 0; margin-block: 10px; }
.publication-attachments h3 { margin: 0 0 6px; font-size: 13px; }
.publication-attachments ul { list-style: none; padding: 0; margin: 0; }
.publication-attachments li { display: flex; flex-wrap: wrap; align-items: center; gap: 8px 12px;
  padding: 6px 0; border-bottom: 1px solid var(--line); }
.publication-file { min-width: 0; flex: 1 1 9rem; overflow-wrap: anywhere; }
.publication-file small { margin-inline-start: 8px; white-space: nowrap; }
.remove-file { color: var(--c-danger); }
</style>
