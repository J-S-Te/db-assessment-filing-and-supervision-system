<script setup lang="ts">
import { impactDamages, impactSubjects, type ClassificationAssessment } from '../lib/classification'

defineProps<{ assessment: ClassificationAssessment; declared?: string }>()
</script>

<template>
  <section class="panel detail-panel" aria-label="定级自评依据">
    <header class="section-heading"><h2>定级自评依据</h2><small>{{ assessment.standard }}</small></header>
    <dl class="detail-facts">
      <div><dt>业务信息安全</dt><dd>{{ assessment.informationLevel }} 级</dd></div>
      <div><dt>系统服务安全</dt><dd>{{ assessment.serviceLevel }} 级</dd></div>
      <div><dt>初步建议</dt><dd>{{ assessment.suggestedLevel }} 级</dd></div>
      <div v-if="assessment.expertReview"><dt>专家评审</dt><dd>需组织评审</dd></div>
      <div v-if="assessment.competentAuthority"><dt>主管部门</dt><dd>{{ assessment.competentAuthority }}</dd></div>
      <div v-if="assessment.filingAuthority"><dt>备案机关</dt><dd>{{ assessment.filingAuthority }}</dd></div>
    </dl>
    <p v-if="declared && Number(declared) !== assessment.suggestedLevel" class="warning-text">
      申报等级 {{ declared }} 级与初步建议不同，请核对定级材料。
    </p>
    <ul class="classification-evidence">
      <li v-for="impact in assessment.information" :key="'information-' + impact.subject">
        业务信息安全 · {{ impactSubjects[impact.subject] }} · {{ impactDamages[impact.damage] }}
      </li>
      <li v-for="impact in assessment.service" :key="'service-' + impact.subject">
        系统服务安全 · {{ impactSubjects[impact.subject] }} · {{ impactDamages[impact.damage] }}
      </li>
    </ul>
    <p class="classification-basis">{{ assessment.basis }}</p>
  </section>
</template>

<style scoped>
.classification-evidence { margin: .5rem 1rem; padding-inline-start: 1.25rem; }
.classification-basis, .warning-text { margin: .75rem 1rem; white-space: pre-wrap; overflow-wrap: anywhere; }
.warning-text { color: var(--c-warning, #9a5709); }
</style>
