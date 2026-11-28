package supervision

import (
	"context"
	"errors"
	"math"
	"net/http"
	"slices"
	"strconv"

	"github.com/jackc/pgx/v5"

	"grade-business/internal/access"
	"grade-business/internal/cases"
	"grade-business/internal/httpx"
)

// registerSiteAudience 将现场检查入口挂载在已经隔离的两端任务路径下。
// 参数 mux、base、operator 为服务路由、任务地址和办理端。
// 返回值 无。
func (s *Service) registerSiteAudience(mux *http.ServeMux, base string, operator bool) {
	path := base + "/site-inspection"
	mux.HandleFunc("GET "+path, taskRequest(operator, s.siteHTTP))
	mux.HandleFunc("POST "+path+"/actions", taskRequest(operator, s.siteActionHTTP))
	mux.HandleFunc("POST "+path+"/attachments", taskRequest(operator, s.siteUploadHTTP))
	mux.HandleFunc("GET "+path+"/attachments/{attachmentId}", taskRequest(operator, s.siteDownloadHTTP))
}

// siteHTTP 读取当前收件单位可看到的现场计划和办理轨迹。
// 参数 w、r、id、owner 为响应、请求、监督任务及运营单位。
// 返回值 无。
func (s *Service) siteHTTP(w http.ResponseWriter, r *http.Request, id, owner string) {
	result, err := s.siteDetail(r.Context(), id, owner)
	respond(w, 200, result, err)
}

// siteActionHTTP 提交一次严格类型化的现场检查、跟进或办结决定。
// 参数 w、r、id、owner 为响应、请求、任务及当前范围。
// 返回值 无。
func (s *Service) siteActionHTTP(w http.ResponseWriter, r *http.Request, id, owner string) {
	var input siteActionInput
	if httpx.Decode(w, r, &input) != nil || !validateSiteAction(&input, owner != "") {
		respond(w, 200, nil, errInput)
		return
	}
	result, err := s.executeSiteAction(r.Context(), id, owner, input)
	respond(w, 200, result, err)
}

// knownSiteKind 核对证据类别及对应的材料提交方。
// 参数 owner 为可选运营单位，kind 为外来材料类型。
// 返回值 当前办理方可上传此类材料时为真。
func knownSiteKind(owner, kind string) bool {
	return owner == "" && kind == siteEvidence || owner != "" && kind == siteFollowUpEvidence
}

// siteUploadHTTP 受限读取一份 PDF 或 DOCX 现场或跟进证据。
// 参数 w、r、id、owner 为响应、请求、已校验任务及可选运营单位。
// 返回值 无。
func (s *Service) siteUploadHTTP(w http.ResponseWriter, r *http.Request, id, owner string) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBody)
	err := r.ParseMultipartForm(1 << 20)
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	if err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			err = cases.ErrFileSize
		} else {
			err = errInput
		}
		respond(w, 201, nil, err)
		return
	}
	result, err := s.uploadSiteMaterial(r, id, owner)
	respond(w, 201, result, err)
}

// uploadSiteMaterial 核对授权后暂存并验证实际证据内容。
// 参数 r 是已限制大小的唯一附件表单，id、owner 为任务与范围。
// 返回值 有效材料与其独立现场版本，或格式及工作流错误。
func (s *Service) uploadSiteMaterial(r *http.Request, id, owner string) (attachmentMutation, error) {
	form := r.MultipartForm
	if len(form.Value) != 2 || len(form.Value["kind"]) != 1 || len(form.Value["expectedVersion"]) != 1 ||
		len(form.File) != 1 || len(form.File["file"]) != 1 {
		return attachmentMutation{}, errInput
	}
	version, err := strconv.ParseInt(form.Value["expectedVersion"][0], 10, 64)
	kind := form.Value["kind"][0]
	if err != nil || version < 1 || version == math.MaxInt64 || !knownSiteKind(owner, kind) {
		return attachmentMutation{}, errInput
	}
	task, err := loadTask(r.Context(), s.db, id, owner, false)
	if err != nil {
		return attachmentMutation{}, err
	}
	if task.Historical {
		return attachmentMutation{}, pgx.ErrNoRows
	}
	header := form.File["file"][0]
	file, err := header.Open()
	if err != nil {
		return attachmentMutation{}, errAttachment
	}
	defer file.Close()
	item, key, digest, err := s.files.StagePublicationAttachment(header.Filename, file)
	if err != nil {
		return attachmentMutation{}, err
	}
	defer s.discardUnused(r.Context(), key)
	item.Kind = kind
	return s.persistSiteMaterial(r.Context(), id, owner, version, item, key, digest)
}

// persistSiteMaterial 再次锁定归属、阶段及版本，在事务内写入文件引用。
// 参数 ctx、id、owner、version 控制业务操作，file、key、digest 为已验证材料。
// 返回值 唯一有效文件或原重复结果，以及错误信息。
func (s *Service) persistSiteMaterial(ctx context.Context, id, owner string, version int64,
	file cases.Attachment, key, digest string,
) (attachmentMutation, error) {
	var result attachmentMutation
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		task, err := loadTask(ctx, tx, id, owner, true)
		if err != nil {
			return err
		}
		record, err := loadSite(ctx, tx, id, false)
		if err != nil {
			return err
		}
		if err = lockWritableFiling(ctx, tx, task, false); err != nil {
			return err
		}
		if !slices.Contains(siteActions(task, record, owner != "", true), "upload") {
			return errState
		}
		result, err = saveSiteFile(ctx, tx, task.ID, record, version, file, key, digest)
		return err
	})
	return result, err
}

// saveSiteFile 保留真实版本，最新文件字节相同时复用原记录。
// 参数 ctx、tx 为写事务，id、record、version 描述任务，file、key、digest 是材料数据。
// 返回值 现有或新写入的材料，或版本冲突和数据库错误。
func saveSiteFile(ctx context.Context, tx pgx.Tx, id string, record siteRecord, version int64,
	file cases.Attachment, key, digest string,
) (attachmentMutation, error) {
	var old cases.Attachment
	var oldDigest string
	err := tx.QueryRow(ctx, `SELECT `+materialColumns+`,a.digest FROM supervision_site_attachments a
		WHERE a.task_id=$1 AND a.kind=$2 AND a.version>$3 ORDER BY version DESC LIMIT 1`,
		id, file.Kind, record.historyVersion).
		Scan(append(materialTargets(&old), &oldDigest)...)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return attachmentMutation{}, err
	}
	if oldDigest == digest && version <= record.Version {
		return attachmentMutation{old, record.Version}, nil
	}
	if record.Version != version {
		return attachmentMutation{}, errState
	}
	version++
	err = tx.QueryRow(ctx, `INSERT INTO supervision_site_attachments
		(task_id,kind,file_name,file_key,digest,size_bytes,version) VALUES($1,$2,$3,$4,$5,$6,$7)
		RETURNING id::text,uploaded_at`, id, file.Kind, file.FileName, key, digest, file.Size, version).Scan(&file.ID, &file.UploadedAt)
	if err != nil {
		return attachmentMutation{}, err
	}
	_, err = tx.Exec(ctx, `UPDATE supervision_site_inspections SET version=$2 WHERE task_id=$1`, id, version)
	return attachmentMutation{file, version}, err
}

const visibleSiteFile = `
	((a.kind='inspection_evidence' AND $2='') OR
	 (a.kind='follow_up_evidence' AND $2<>'') OR EXISTS(
	    SELECT 1 FROM supervision_site_events e WHERE e.task_id=a.task_id AND e.evidence_id=a.id
	 ))`

// latestSiteAttachments 展示本端草稿与另一端正式登记的可下载材料。
// 参数 ctx、db 控制查询，id、owner、versions 为已核验任务、席位和历史范围。
// 返回值 可见的最新材料或数据库错误。
func latestSiteAttachments(ctx context.Context, db reader, id, owner string,
	versions inspectionVersionRange,
) ([]cases.Attachment, error) {
	rows, err := db.Query(ctx, `SELECT DISTINCT ON (a.kind) `+materialColumns+` FROM supervision_site_attachments a
		WHERE a.task_id=$1 AND `+visibleSiteFile+` AND a.version>$3 AND ($4::bigint=0 OR a.version<=$4)
		ORDER BY a.kind,a.version DESC`, id, owner, versions.after, versions.through)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[cases.Attachment])
}

// siteDownloadHTTP 逐次核验附件、原收件单位、提交记录和同任务归属。
// 参数 w、r、id、owner 为响应、下载请求与收件范围。
// 返回值 无。
func (s *Service) siteDownloadHTTP(w http.ResponseWriter, r *http.Request, id, owner string) {
	attachment := r.PathValue("attachmentId")
	if !validID(attachment) {
		respond(w, 200, nil, errInput)
		return
	}
	var item cases.Attachment
	var key string
	err := s.db.QueryRow(r.Context(), `SELECT `+materialColumns+`,a.file_key FROM supervision_site_attachments a
		JOIN supervision_tasks t ON t.id=a.task_id
		JOIN supervision_site_inspections s ON s.task_id=t.id
		WHERE t.id=$1 AND `+readableScope+` AND a.id=$3 AND `+visibleSiteFile+`
		AND `+access.FilingDistrictScope("t.filing_id", "$4", false)+`
		AND CASE WHEN `+originalTaskOwner+` THEN a.version<=s.merge_history_version
		         WHEN $2<>'' THEN a.version>COALESCE(s.merge_history_version,0) ELSE true END`,
		id, owner, attachment, access.Districts(r.Context())).
		Scan(append(materialTargets(&item), &key)...)
	if err == nil {
		err = s.files.ServeBusinessAttachment(w, r, item, key)
	}
	if err != nil {
		respond(w, 200, nil, err)
	}
}
