<script setup lang="ts">
import { computed } from 'vue'
import { filingPolicyKinds, type FilingMaterialRule } from '../lib/filingPolicy'

const props = defineProps<{
  rules: FilingMaterialRule[]
  files: { kind: string; fileName: string }[]
}>()
const rows = computed(() => props.rules.map(rule => {
  const token = (rule.filenameContains ?? '').toLowerCase()
  const covered = rule.stage === 'initial' && !token
    && (rule.kind === 'filing_form' || rule.kind === 'classification_report')
  const match = props.files.find(file =>
    (file.kind === rule.kind && file.fileName.toLowerCase().includes(token))
    || (covered && file.kind === 'filing_package'))
  return { rule, match }
}))
</script>

<template>
  <section v-if="rows.length" class="panel detail-panel" aria-label="附加必传材料">
    <header class="panel-heading panel-compact"><h2>附加必传材料</h2></header>
    <ul class="policy-results">
      <li v-for="{ rule, match } in rows" :key="rule.stage + ':' + rule.label">
        <strong>{{ rule.label }}</strong>
        <span class="muted"> · {{ filingPolicyKinds[rule.stage][rule.kind] }}
          <template v-if="rule.filenameContains">，文件名包含「{{ rule.filenameContains }}」</template>
        </span>
        <span v-if="match"> · 已上传 {{ match.fileName }}</span>
        <span v-else class="warning-text"> · 待上传</span>
      </li>
    </ul>
  </section>
</template>

<style scoped>
.policy-results { margin: .75rem 1rem; padding-inline-start: 1.25rem; display: grid; gap: .4rem; }
.warning-text { color: var(--c-warning, #9a5709); }
</style>
