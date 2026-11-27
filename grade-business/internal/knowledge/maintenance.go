package knowledge

import (
	"net/http"

	"github.com/jackc/pgx/v5"
	"grade-business/internal/httpx"
	"grade-business/internal/tagcatalog"
)

const entryExists = `
	SELECT COALESCE((SELECT version FROM updated), 0),
	       EXISTS (SELECT 1 FROM knowledge_entries WHERE id = $1)
`

// update 保存知识正文和来源，保留本条目的标签。
// 参数 w 为响应写入端，r 包含条目标识、已读版本和更新后的内容。
// 返回值 无，成功返回新版本，冲突时保留已存条目。
func (s *Service) update(w http.ResponseWriter, r *http.Request) {
	id, valid := parseID(r.PathValue("id"))
	if !valid {
		httpx.Error(w, http.StatusNotFound, "not_found", "知识条目不存在。")
		return
	}
	var input updateInput
	if httpx.Decode(w, r, &input) != nil || input.ExpectedVersion < 1 {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "请提交有效的知识条目和版本。")
		return
	}
	item, err := validateCreate(input.createInput)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	row := s.revise(r.Context(), id, input.ExpectedVersion, "edit",
		"title = $4, category = $5, source = $6, body = $7", item.Title, item.Category, item.Source, item.Body)
	respondUpdate(w, row)
}

// updateTags 替换单条知识的标签，不修改条目正文和来源。
// 参数 w 为响应写入端，r 包含条目标识、已读版本和新的标签列表。
// 返回值 无，成功返回新版本，冲突时保留已存标签。
func (s *Service) updateTags(w http.ResponseWriter, r *http.Request) {
	id, valid := parseID(r.PathValue("id"))
	if !valid {
		httpx.Error(w, http.StatusNotFound, "not_found", "知识条目不存在。")
		return
	}
	var input tagsInput
	if httpx.Decode(w, r, &input) != nil || input.ExpectedVersion < 1 {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "请提交有效的标签和条目版本。")
		return
	}
	tags, err := normalizeTags(input.Tags)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	row := s.revise(r.Context(), id, input.ExpectedVersion, "tags", "tags = $4", tags)
	respondUpdate(w, row)
}

// respondUpdate 区分维护成功、停用标签冲突、条目已变更和条目不存在。
// 参数 w 为响应写入端，row 为原子更新返回的新版本和存在状态。
// 返回值 无。
func respondUpdate(w http.ResponseWriter, row pgx.Row) {
	var result updateResult
	var exists bool
	err := row.Scan(&result.Version, &exists)
	switch {
	case tagcatalog.IsDisabledAssignment(err):
		httpx.Error(w, http.StatusConflict, "tag_disabled", "不能新增已停用的标签，请从标签目录选择启用的标签。")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "知识条目暂时无法保存。")
	case result.Version > 0:
		httpx.JSON(w, http.StatusOK, result)
	case exists:
		httpx.Error(w, http.StatusConflict, "version_conflict", "条目已被更新，请重新加载后核对修改。")
	default:
		httpx.Error(w, http.StatusNotFound, "not_found", "知识条目不存在。")
	}
}
