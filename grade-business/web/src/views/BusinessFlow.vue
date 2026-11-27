<script setup lang="ts">
const steps = [
  { label: '系统定级', to: { path: '/filings/new', query: { classification: '1' } } },
  { label: '备案（预审 → 初审 → 终审）', to: '/filings' },
  { label: '实施报备', to: '/implementations' },
  { label: '测评（机构测 → 监管审）', to: '/reports' },
  { label: '整改规划审核（运营者报 → 监管审）', to: '/remediations' },
  { label: '整改闭环', to: '/hazards' },
  { label: '持续监测', to: '/alerts' },
]
</script>

<template>
  <section aria-labelledby="flow-title">
    <header class="v2-page-head">
      <div><h1 id="flow-title">流程示意</h1><p>等级保护全流程 · 监管视角</p></div>
    </header>
    <section class="panel flow-panel" aria-labelledby="flow-steps">
      <h2 id="flow-steps">等级保护全流程</h2>
      <ol class="flow-steps">
        <li v-for="(step, index) in steps" :key="step.label">
          <RouterLink class="button neutral" :to="step.to">{{ index + 1 }}. {{ step.label }}</RouterLink>
          <span v-if="index + 1 < steps.length" aria-hidden="true">→</span>
        </li>
      </ol>
      <div class="flow-roles">
        <article>
          <h3>监管职责</h3>
          <ul><li>核验备案材料与定级，完成初审和终审。</li>
            <li>复核实施报备、测评报告和整改规划，填写审批意见。</li>
            <li>复核整改证据、确认销项，并处置站内预警。</li></ul>
        </article>
        <article>
          <h3>机构与运营者职责</h3>
          <ul><li>机构执行测评，按审核意见补正备案材料、实施报备和测评报告。</li>
            <li>运营者编制整改规划，查收反馈、提交整改证据。</li>
            <li>双方按记录当前阶段办理，签收和补正历史可查询。</li></ul>
        </article>
      </div>
    </section>
  </section>
</template>

<style scoped>
.flow-panel { padding: 18px; }
.flow-steps { display: flex; gap: 6px 8px; flex-wrap: wrap; list-style: none; padding: 0; }
.flow-steps li { display: flex; align-items: center; gap: 8px; }
.flow-steps span { color: var(--muted); }
.flow-roles { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(280px, 100%), 1fr)); gap: 12px; }
.flow-roles article { border: 1px solid var(--line); border-radius: var(--radius); padding: 12px; }
.flow-roles h3 { margin: 0; }
.flow-roles ul { margin-bottom: 0; padding-left: 1.2rem; }
.flow-roles li + li { margin-top: 6px; }
</style>
