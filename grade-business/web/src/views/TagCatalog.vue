<script setup lang="ts">
import { reactive, ref, shallowRef } from 'vue'
import { Pencil, Plus, Search } from '@lucide/vue'
import { useRoute } from 'vue-router'
import AsyncState from '../components/AsyncState.vue'
import { api, ApiError, errorText } from '../lib/api'
import { queryValue, usePagedList } from '../lib/paging'
import { defaultTagColor, tagCatalogPath, type TagCatalogEntry } from '../lib/tagCatalog'

const route = useRoute()
const { keyword, filter, page, failure, loading, load, search, reset, nextPage, previousPage } =
  usePagedList<TagCatalogEntry>(() => tagCatalogPath, 'status')
const dialog = ref<HTMLDialogElement>()
const editing = shallowRef<TagCatalogEntry>()
const form = reactive({ name: '', color: defaultTagColor, active: true })
const saving = ref(false)
const formError = ref('')
const notice = ref('')
const actionError = ref('')

/**
 * 用途 根据当前目录记录打开新增或编辑窗口。
 * 参数 item 为要编辑的记录，省略时新增。
 * 返回值 无。
 */
function openTag(item?: TagCatalogEntry) {
  editing.value = item
  form.name = item?.name ?? ''
  form.color = item?.color ?? defaultTagColor
  form.active = item?.active ?? true
  formError.value = ''
  actionError.value = ''
  notice.value = ''
  dialog.value?.showModal()
}

/**
 * 用途 新建目录标签或按打开时的版本保存颜色与启停状态。
 * 参数 无，使用当前确认表单。
 * 返回值 保存和刷新当前目录页的任务。
 */
async function saveTag() {
  if (saving.value) return
  const item = editing.value
  const name = form.name.trim()
  if (!item && (!name || Array.from(name).length > 32)) {
    formError.value = '请输入 1 到 32 字符的标签名称。'
    return
  }
  saving.value = true
  formError.value = ''
  try {
    const result = item ? await api<TagCatalogEntry>(tagCatalogPath + '/' + encodeURIComponent(item.id), {
      method: 'PATCH', body: { color: form.color, active: form.active, expectedVersion: item.version },
    }) : await api<TagCatalogEntry>(tagCatalogPath, { method: 'POST', body: { name, color: form.color } })
    dialog.value?.close()
    notice.value = `已保存标签「${result.name}」。`
    await load()
  } catch (error) {
    if (error instanceof ApiError && error.code === 'version_conflict') {
      dialog.value?.close()
      actionError.value = errorText(error)
      await load()
    } else {
      formError.value = errorText(error)
    }
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <section class="v2-list" aria-labelledby="tag-catalog-heading">
    <header class="page-header">
      <div><h2 id="tag-catalog-heading">标签管理</h2><p>维护知识标签的颜色与使用状态</p></div>
    </header>
    <form class="v2-list-toolbar" @submit.prevent="search">
      <div class="v2-toolbar-row">
        <button type="button" class="button primary" @click="openTag()"><Plus :size="16" aria-hidden="true" />新增标签</button>
        <span class="v2-toolbar-spacer"></span>
        <label class="field"><span>状态</span><select v-model="filter">
          <option value="">全部状态</option><option value="active">启用</option><option value="disabled">停用</option>
        </select></label>
        <label class="field filter-keyword"><span>标签名称</span>
          <input v-model="keyword" type="search" maxlength="120" placeholder="检索标签">
        </label>
        <button type="submit" class="button primary"><Search :size="16" aria-hidden="true" />查询</button>
        <button type="button" class="button neutral" @click="reset">重置</button>
      </div>
    </form>

    <p v-if="notice" class="form-message success-message" role="status">{{ notice }}</p>
    <p v-if="actionError" class="form-message error-message" role="alert">{{ actionError }}</p>
    <AsyncState :loading="loading" :error="failure" :empty="!page.items.length"
      empty-text="没有找到符合条件的知识标签。" @retry="load">
      <div class="table-wrap v2-list-table">
        <table class="data-table">
          <thead><tr><th>标签</th><th>颜色</th><th>知识使用数</th><th>状态</th><th>操作</th></tr></thead>
          <tbody>
            <tr v-for="item in page.items" :key="item.id">
              <td><strong class="emphasis">{{ item.name }}</strong></td>
              <td><span class="tag-color"><span class="tag-swatch" :style="{ backgroundColor: item.color }" aria-hidden="true"></span>
                <span class="code">{{ item.color }}</span></span></td>
              <td>{{ item.usageCount }}</td>
              <td><span class="status-badge" :class="item.active ? 'status-approved' : 'status-draft'">
                {{ item.active ? '启用' : '停用' }}</span></td>
              <td><button class="text-action" type="button" @click="openTag(item)">
                <Pencil :size="14" aria-hidden="true" />编辑</button></td>
            </tr>
          </tbody>
        </table>
      </div>
    </AsyncState>
    <footer v-if="queryValue(route.query.cursor) || page.nextCursor" class="panel-footer pager">
      <span>本页 {{ page.items.length }} 条</span>
      <div>
        <button type="button" class="button neutral small" :disabled="loading || !queryValue(route.query.cursor)"
          @click="previousPage">上一页</button>
        <button type="button" class="button neutral small" :disabled="loading || !page.nextCursor" @click="nextPage">下一页</button>
      </div>
    </footer>

    <dialog ref="dialog" class="decision-dialog" aria-labelledby="edit-catalog-tag" @cancel.prevent="!saving && dialog?.close()">
      <form @submit.prevent="saveTag">
        <h2 id="edit-catalog-tag">{{ editing ? '编辑标签' : '新增标签' }}</h2>
        <label class="field"><span>标签名称</span>
          <input v-model="form.name" required maxlength="64" :readonly="!!editing" :disabled="saving">
        </label>
        <label class="field"><span>颜色</span><input v-model="form.color" type="color" :disabled="saving"></label>
        <label v-if="editing" class="field"><span>状态</span><select v-model="form.active" :disabled="saving">
          <option :value="true">启用</option><option :value="false">停用</option>
        </select></label>
        <p v-if="editing && !form.active" class="muted">停用后无法新增该标签的关联，已有知识仍可查看原标签。</p>
        <p v-if="formError" class="form-message error-message" role="alert">{{ formError }}</p>
        <div class="form-actions">
          <button type="button" class="button neutral" :disabled="saving" @click="dialog?.close()">取消</button>
          <button type="submit" class="button primary" :disabled="saving">{{ saving ? '保存中…' : '保存标签' }}</button>
        </div>
      </form>
    </dialog>
  </section>
</template>

<style scoped>
.tag-color { display: inline-flex; align-items: center; gap: 8px; }
.tag-swatch { width: 14px; height: 14px; border: 1px solid var(--line); border-radius: 4px; }
.decision-dialog .field { margin: 12px 0; }
.decision-dialog input[type="color"] { width: 64px; cursor: pointer; }
</style>
