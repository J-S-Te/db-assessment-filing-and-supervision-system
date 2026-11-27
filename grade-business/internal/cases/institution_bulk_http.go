package cases

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"grade-business/internal/httpx"
)

// registerInstitutionBulk 挂载机构备案批处理、复制和测评台账下载。
// 参数 mux 为服务业务路由。
// 返回值 无。
func (s *Service) registerInstitutionBulk(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/institution/filings/{id}/copy", withInstitution(s.institutionCopyHTTP))
	mux.HandleFunc("POST /api/v1/institution/filings/batch", withInstitution(s.institutionBatchHTTP))
	mux.HandleFunc("GET /api/v1/institution/reports/export", s.downloadInstitutionReports)
}

// institutionCopyHTTP 用途为从当前机构范围复制备案记录，支持可选预填编号。
// 参数 w、r 为响应及请求，scope 是已经校验的机构名。
// 返回值 无，成功时返回草稿摘要。
func (s *Service) institutionCopyHTTP(w http.ResponseWriter, r *http.Request, scope string) {
	var input filingCopyInput
	id := strings.ToLower(r.PathValue("id"))
	if !validID(id) {
		respondError(w, ErrNotFound)
		return
	}
	if httpx.Decode(w, r, &input) != nil || !validID(input.RequestID) || input.ExpectedVersion < 1 {
		respondError(w, ErrInvalid)
		return
	}
	input.RequestID = strings.ToLower(input.RequestID)
	input.Number = strings.TrimSpace(input.Number)
	if input.Number != "" && !validText(input.Number, 80) {
		respondError(w, ErrInvalid)
		return
	}
	payload := struct {
		SourceID string
		filingCopyInput
	}{id, input}
	item, err := institutionCommand(r.Context(), s.db, scope, input.RequestID, "copy", payload,
		func(tx pgx.Tx) (mutation, error) { return copyInstitutionFiling(r.Context(), tx, scope, id, input) })
	respond(w, http.StatusCreated, item, err)
}

// institutionBatchHTTP 对明确选择、携带版本的机构记录执行批量操作。
// 参数 w、r 为响应和请求，scope 是已校验机构。
// 返回值 无，逐条成功与可恢复的拒绝结果均返回。
func (s *Service) institutionBatchHTTP(w http.ResponseWriter, r *http.Request, scope string) {
	var input filingBatchInput
	if httpx.Decode(w, r, &input) != nil || !input.valid() {
		respondError(w, ErrInvalid)
		return
	}
	item, err := institutionCommand(r.Context(), s.db, scope, input.RequestID, input.Action, input,
		func(tx pgx.Tx) (itemResponse[filingBatchOutcome], error) {
			return s.runInstitutionBatch(r.Context(), tx, scope, input)
		})
	respond(w, http.StatusOK, item, err)
}

// institutionCommand 保存同一机构的请求指纹和实际结果，防止并发重放重复写入。
// 参数 ctx、db 为请求及数据库，scope、id、command、payload 指定命令，execute 执行新操作。
// 返回值 首次保存或重放的结果，或重放输入不一致及数据库错误。
func institutionCommand[T any](ctx context.Context, db interface {
	Begin(context.Context) (pgx.Tx, error)
}, scope, id, command string, payload any, execute func(pgx.Tx) (T, error)) (T, error) {
	var result T
	input, err := json.Marshal(payload)
	if err != nil {
		return result, err
	}
	digest := sha256.Sum256(input)
	fingerprint := hex.EncodeToString(digest[:])
	err = pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		inserted, err := tx.Exec(ctx, `INSERT INTO institution_filing_commands(institution,request_id,command,input_digest)
			VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, scope, id, command, fingerprint)
		if err != nil {
			return err
		}
		if inserted.RowsAffected() == 0 {
			return replayInstitutionCommand(ctx, tx, scope, id, command, fingerprint, &result)
		}
		result, err = execute(tx)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE institution_filing_commands SET result=$3
			WHERE institution=$1 AND request_id=$2`, scope, id, result)
		return err
	})
	return result, err
}

// replayInstitutionCommand 只为相同输入返回原操作记录，禁止复用请求标识更改命令。
// 参数 ctx、tx 为事务，scope、id、command、digest 指定原操作，dest 接收结果。
// 返回值 结果读取错误或请求标识冲突。
func replayInstitutionCommand[T any](ctx context.Context, tx pgx.Tx, scope, id, command, digest string, dest *T) error {
	var priorCommand, priorDigest string
	var data []byte
	err := tx.QueryRow(ctx, `SELECT command,input_digest,result FROM institution_filing_commands
		WHERE institution=$1 AND request_id=$2`, scope, id).Scan(&priorCommand, &priorDigest, &data)
	if err != nil {
		return err
	}
	if priorCommand != command || priorDigest != digest {
		return ErrConflict
	}
	return json.Unmarshal(data, dest)
}
