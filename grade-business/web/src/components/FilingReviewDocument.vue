<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { api, attachmentURL, errorText, isAbort, params } from '../lib/api'
import type { Attachment } from '../lib/domain'
import { canPreviewWord, wordPreviewParagraph,
  type FilingTextDocument, type WordEvidenceTarget } from '../lib/filingReviewEvidence'

const props = defineProps<{ path: string; attachments: Attachment[]; target?: WordEvidenceTarget }>()
const emit = defineEmits<{ clearCitation: []; returnToCitation: []; geometry: [] }>()
const attachmentId = ref('')
const member = ref<string>()
const documents = shallowRef<FilingTextDocument[]>([])
const loading = ref(false)
const failure = ref('')
const paper = ref<HTMLElement>()
const attachment = computed(() => props.attachments.find(item => item.id === attachmentId.value))
const supported = computed(() => attachment.value && canPreviewWord(attachment.value, member.value))
const targetDocument = computed(() => documents.value.find(document => document.member === props.target?.member))
const located = computed(() => props.target?.attachment.id === attachmentId.value &&
  props.target?.paragraph && targetDocument.value &&
  wordPreviewParagraph(targetDocument.value, props.target.paragraph) !== undefined)
let request: AbortController | undefined

/**
 * 用途 查询当前备案附件或归档成员的文字预览。
 * 参数 无，读取范围来自当前工作区与已选择附件。
 * 返回值 预览请求任务。
 */
async function read() {
  request?.abort()
  const current = new AbortController()
  request = current
  documents.value = []
  failure.value = ''
  loading.value = Boolean(supported.value)
  if (!supported.value) return
  const query = params({ review: 1, member: member.value })
  const url = `/attachments/${encodeURIComponent(attachmentId.value)}/text${query}`
  try {
    const result = await api<{ items: FilingTextDocument[] }>(props.path + url, { signal: current.signal })
    if (request === current) documents.value = result.items
  } catch (error) {
    if (request === current && !isAbort(error)) failure.value = errorText(error)
  } finally {
    if (request === current) loading.value = false
  }
}

/**
 * 用途 判断一个实际渲染的标题或段落是否为当前证据。
 * 参数 document 为预览文档，paragraph 为该位置的原始段号。
 * 返回值 该段可以当作连线锚点时为真。
 */
function isAnchor(document: FilingTextDocument, paragraph?: number) {
  return Boolean(located.value && document === targetDocument.value && paragraph === props.target?.paragraph)
}

/**
 * 用途 切换手动选择的附件并解除先前批注的定位。
 * 参数 id 为当前备案附件 ID。
 * 返回值 无。
 */
function chooseAttachment(id: string) {
  emit('clearCitation')
  attachmentId.value = id
  member.value = undefined
}

/**
 * 用途 将匹配到的正文段落滚动到预览中部，窄屏时同步带入窗口视野。
 * 参数 无，滚动目标由当前证据决定。
 * 返回值 页面完成渲染后的滚动任务。
 */
async function revealAnchor() {
  await nextTick()
  const viewport = paper.value
  const anchor = viewport?.querySelector<HTMLElement>('[data-review-anchor]')
  if (anchor && viewport) {
    const offset = anchor.getBoundingClientRect().top - viewport.getBoundingClientRect().top
    viewport.scrollTop += offset - viewport.clientHeight / 3
    if (matchMedia('(max-width: 860px)').matches) anchor.scrollIntoView({ block: 'nearest' })
  }
  emit('geometry')
}

watch(() => props.attachments, attachments => {
  if (attachments.some(item => item.id === attachmentId.value)) return
  attachmentId.value = (attachments.find(item => canPreviewWord(item)) ?? attachments[0])?.id ?? ''
  member.value = undefined
}, { immediate: true })
watch(() => props.target, target => {
  if (!target) return
  attachmentId.value = target.attachment.id
  member.value = target.member
}, { immediate: true })
watch([() => props.path, attachmentId, member], () => void read(), { immediate: true })
watch([() => props.target, documents], () => void revealAnchor(), { flush: 'post' })
onBeforeUnmount(() => request?.abort())
</script>

<template>
  <section class="word-review pane" aria-labelledby="review-document-title">
    <header class="word-review-toolbar">
      <h3 id="review-document-title">材料原文</h3>
      <label v-if="attachments.length" class="document-picker">
        <span>附件</span>
        <select :value="attachmentId" @change="chooseAttachment(($event.target as HTMLSelectElement).value)">
          <option v-for="file in attachments" :key="file.id" :value="file.id">{{ file.fileName }}</option>
        </select>
      </label>
      <a v-if="attachment" class="text-action" :href="attachmentURL(path, attachment.id)">下载原件</a>
      <button v-if="target" type="button" class="text-action return-to-citation" @click="emit('returnToCitation')">
        返回批注
      </button>
    </header>
    <p v-if="member" class="member-path muted">归档成员 · {{ member }}</p>
    <div ref="paper" class="word-review-scroll" data-review-scroll tabindex="0" aria-label="材料文字预览">
      <p v-if="loading" class="muted" role="status">正在读取 Word 正文和表格文字…</p>
      <p v-else-if="failure" class="error-message" role="alert">
        {{ failure }} <button class="text-action" type="button" @click="read">重试</button>
      </p>
      <p v-else-if="!attachment" class="muted">暂无附件。</p>
      <p v-else-if="!supported" class="muted">该材料可下载原件核对。</p>
      <template v-else>
        <p v-if="target && !located" class="muted" role="status">
          本次依据无法定位到可预览的 Word 段落，请核对原件或右侧摘录。
        </p>
        <article v-for="document in documents" :key="document.member ?? document.fileName" class="word-review-paper">
          <h4>{{ document.fileName }}</h4>
          <p v-if="document.message" class="muted">{{ document.message }}</p>
          <p v-if="document.truncated" class="muted">当前展示部分文字，完整内容请查看原件。</p>
          <section v-for="(section, index) in document.sections" :key="index">
            <h5 :data-review-anchor="isAnchor(document, section.titleParagraph) ? '' : undefined">
              {{ section.title }}
            </h5>
            <p v-for="(paragraph, number) in section.paragraphs" :key="number"
              :data-review-anchor="isAnchor(document, section.paragraphNumbers?.[number]) ? '' : undefined">
              {{ paragraph }}
            </p>
          </section>
        </article>
      </template>
    </div>
  </section>
</template>

<style scoped>
.word-review { min-width: 0; border: 1px solid var(--line); border-radius: 12px; overflow: hidden; }
.word-review-toolbar { display: flex; align-items: center; flex-wrap: wrap; gap: .55rem .85rem;
  min-height: var(--review-toolbar-height); padding: .65rem 1rem; }
.word-review-toolbar h3 { margin: 0; font-size: 1rem; }
.document-picker { display: flex; align-items: center; gap: .45rem; flex: 1; min-width: 12rem; }
.document-picker span { white-space: nowrap; }
.document-picker select { width: 100%; min-width: 0; }
.member-path { padding-inline: 1rem; margin: 0 0 .55rem; overflow-wrap: anywhere; }
.word-review-scroll { height: var(--review-pane-height); overflow: auto; padding: 1rem; background: var(--bg, #eef3f8); }
.word-review-paper { background: var(--surface, #fff); border: 1px solid var(--line); border-radius: 8px;
  padding: clamp(1rem, 3vw, 2.2rem); margin-block-end: 1rem; overflow-wrap: anywhere; }
.word-review-paper h4 { margin: 0 0 1rem; font-size: 1.15rem; }
.word-review-paper h5, .word-review-paper p { margin: 0 0 .5rem; padding: .22rem .35rem; white-space: pre-wrap; }
.word-review-paper h5 { margin-block-start: .9rem; font-size: 1rem; }
[data-review-anchor] { background: #fff2ba; color: #192536; border-radius: 4px; box-shadow: inset 3px 0 #ad7518; }
.return-to-citation { display: none; }
@media (max-width: 860px) { .return-to-citation { display: inline-flex; } }
</style>
