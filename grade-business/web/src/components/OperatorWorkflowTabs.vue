<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { queryValue } from '../lib/paging'
import { businessScopes, type BusinessAudience } from '../lib/scopedBusiness'

const props = withDefaults(defineProps<{ group: 'rectification' | 'messages'; audience?: BusinessAudience }>(), {
  audience: 'operator',
})
const route = useRoute()
const scopeQuery = computed(() => {
  const key = businessScopes[props.audience].key
  return { [key]: queryValue(route.query[key]).trim() || undefined }
})
const panels = {
  rectification: {
    label: '测评与整改',
    links: [
      { name: '整改规划上传', path: '/operator/remediations' },
      { name: '漏洞整改闭环', path: '/operator/hazards' },
      { name: '历史对比', path: '/operator/assessment-history' },
    ],
  },
  messages: {
    label: '消息中心',
    links: [
      { name: '通知公告', path: '/operator/publications' },
      { name: '预警消息', path: '/operator/alerts' },
      { name: '审核意见', path: '/operator/feedback' },
    ],
  },
}
const institutionMessages = {
  label: '消息中心',
  links: [
    { name: '消息公告', path: '/institution/publications' },
    { name: '预警消息', path: '/institution/alerts' },
    { name: '消息通知', path: '/institution/feedback' },
  ],
}
const current = computed(() => props.audience === 'institution' ? institutionMessages : panels[props.group])
</script>

<template>
  <nav class="v2-tabs" :aria-label="current.label">
    <RouterLink v-for="item in current.links" :key="item.path" class="v2-tab"
      :class="{ active: item.path === route.path }" :aria-current="item.path === route.path ? 'page' : undefined"
      :to="{ path: item.path, query: scopeQuery }">{{ item.name }}</RouterLink>
  </nav>
</template>
