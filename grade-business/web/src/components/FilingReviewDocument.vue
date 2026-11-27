<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { Download, FileText } from '@lucide/vue'
import { api, attachmentURL, errorText, isAbort, params } from '../lib/api'
import type { Attachment } from '../lib/domain'
import { canPreviewWord, wordParagraphKey, wordPreviewParagraph,
  type FilingTextDocument, type WordEvidenceTarget } from '../lib/filingReviewEvidence'

const props = defineProps<{
  path: string; attachments: Attachment[]; attachment?: Attachment; target?: WordEvidenceTarget
  linkedAnchors: ReadonlySet<string>
}>()
const emit = defineEmits<{ selectAttachment: [id: string]; returnToCitation: []; geometry: [] }>()
const attachmentId = computed(() => props.attachment?.id ?? '')
const member = computed(() => props.target?.attachment.id === attachmentId.value ? props.target?.member : undefined)
const documents = shallowRef<FilingTextDocument[]>([])
const loading = ref(false)
const failure = ref('')
const paper = ref<HTMLElement>()
const supported = computed(() => props.attachment && canPreviewWord(props.attachment, member.value))
const targetDocument = computed(() => documents.value.find(document => document.member === member.value))
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
 * 用途 标记当前可见批注所引用的原文段落。
 * 参数 document、paragraph 为预览文档和原始段号。
 * 返回值 段落被可见批注引用时为真。
 */
function isLinked(document: FilingTextDocument, paragraph?: number) {
  const key = wordParagraphKey(document.member, paragraph)
  return key !== undefined && props.linkedAnchors.has(key)
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

watch([() => props.path, attachmentId, member], () => void read(), { immediate: true })
watch([() => props.target, documents], () => void revealAnchor(), { flush: 'post' })
onBeforeUnmount(() => request?.abort())
</script>

<template>
  <section class="word-review" aria-labelledby="review-document-title">
    <header class="word-review-toolbar">
      <div class="word-review-title">
        <FileText :size="19" aria-hidden="true" /><h3 id="review-document-title">材料原文</h3>
      </div>
      <div v-if="attachments.length" class="document-picker">
        <select aria-label="附件" :value="attachmentId"
          @change="emit('selectAttachment', ($event.target as HTMLSelectElement).value)">
          <option v-for="file in attachments" :key="file.id" :value="file.id">{{ file.fileName }}</option>
        </select>
      </div>
      <a v-if="attachment" class="button neutral small original-download" :href="attachmentURL(path, attachment.id)">
        <Download :size="15" aria-hidden="true" />下载原件
      </a>
      <button v-if="target" type="button" class="text-action return-to-citation" @click="emit('returnToCitation')">
        返回批注
      </button>
    </header>
    <div ref="paper" class="word-review-scroll" data-review-scroll data-review-preview tabindex="0" aria-label="材料文字预览">
      <div v-if="member" class="member-path muted">
        <span>归档成员 · {{ member }}</span>
        <button type="button" class="text-action" @click="emit('selectAttachment', attachmentId)">查看整个材料包</button>
      </div>
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
          <header class="word-paper-heading">
            <FileText :size="21" aria-hidden="true" />
            <h4>{{ document.fileName }}</h4>
          </header>
          <p v-if="document.message" class="muted">{{ document.message }}</p>
          <p v-if="document.truncated" class="muted">当前展示部分文字，完整内容请查看原件。</p>
          <section v-for="(section, index) in document.sections" :key="index" class="word-section">
            <h5 :data-review-paragraph="wordParagraphKey(document.member, section.titleParagraph)"
              :class="{ 'citation-linked': isLinked(document, section.titleParagraph) }"
              :data-review-anchor="isAnchor(document, section.titleParagraph) ? '' : undefined">
              {{ section.title }}
            </h5>
            <p v-for="(paragraph, number) in section.paragraphs" :key="number"
              :data-review-paragraph="wordParagraphKey(document.member, section.paragraphNumbers?.[number])"
              :class="{ 'citation-linked': isLinked(document, section.paragraphNumbers?.[number]) }"
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
.word-review { min-width: 0; border: 1px solid var(--review-border); border-radius: 14px;
  overflow: hidden; box-shadow: 0 6px 18px #1734590d; }
.word-review-toolbar, .word-review-title, .word-paper-heading { display: flex; align-items: center; gap: .65rem; }
.word-review-toolbar { flex-wrap: wrap; min-height: var(--review-toolbar-height); padding: .6rem .9rem;
  background: var(--review-panel); border-bottom: 1px solid var(--review-border); }
.word-review-title { flex: none; }
.word-review-title svg, .word-paper-heading svg { color: var(--review-accent); flex: none; }
.word-review-toolbar h3 { margin: 0; font-size: 1rem; }
.document-picker { flex: 1 1 11rem; min-width: 0; }
.document-picker select { width: 100%; min-width: 0; min-height: 2.25rem; background: #fff; text-overflow: ellipsis; }
.original-download { color: var(--review-accent); white-space: nowrap; }
.member-path { display: flex; flex-wrap: wrap; justify-content: space-between; gap: .45rem;
  margin: 0 auto .65rem; max-width: 64rem; font-size: .86rem; overflow-wrap: anywhere; }
.word-review-scroll { max-height: var(--review-pane-height); overflow: auto; scrollbar-gutter: stable;
  padding: clamp(.8rem, 1.75vw, 1.65rem); background: #e9f0f8; }
.word-review-paper { width: min(100%, 64rem); margin: 0 auto 1rem; overflow-wrap: anywhere;
  padding: clamp(1rem, 2.75vw, 2.6rem); border: 1px solid #dbe5ef; border-radius: 11px;
  background: #fff; box-shadow: 0 5px 18px #24456613; }
.word-paper-heading { align-items: flex-start; padding-block-end: .9rem; margin-block-end: .9rem;
  border-bottom: 1px solid #e1e9f2; }
.word-review-paper h4 { margin: 0; font-size: 1.15rem; line-height: 1.4; }
.word-section + .word-section { margin-block-start: .9rem; }
.word-review-paper h5, .word-review-paper p { margin: 0 0 .35rem; padding: .28rem .6rem;
  white-space: pre-wrap; line-height: 1.65; }
.word-review-paper h5 { font-size: 1rem; color: #244668; }
.citation-linked, [data-review-anchor] { background: #e8f3ff; border-radius: 6px; box-shadow: inset 3px 0 #2387ac; }
[data-review-anchor] { background: #dcecff; box-shadow: inset 3px 0 #285bb5; }
.return-to-citation { display: none; }
@media (max-width: 860px) { .return-to-citation { display: inline-flex; } }
</style>
