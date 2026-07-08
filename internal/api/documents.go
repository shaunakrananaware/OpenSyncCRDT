package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/opensynccrdt/opensynccrdt/internal/storage"
)

// docView is the JSON representation of document metadata.
type docView struct {
	ID        string            `json:"id"`
	Metadata  map[string]string `json:"metadata"`
	LatestSeq int64             `json:"latest_seq"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

func toDocView(d *storage.Document) docView {
	meta := d.Metadata
	if meta == nil {
		meta = map[string]string{}
	}
	return docView{
		ID:        d.ID,
		Metadata:  meta,
		LatestSeq: d.LatestSeq,
		CreatedAt: d.CreatedAt,
		UpdatedAt: d.UpdatedAt,
	}
}

// POST /api/v1/docs
func (a *API) handleCreateDoc(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DocID    string            `json:"doc_id"`
		Metadata map[string]string `json:"metadata"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.DocID == "" {
		writeError(w, http.StatusBadRequest, "doc_id is required")
		return
	}
	if err := a.store.CreateDocument(body.DocID, body.Metadata); err != nil {
		if errors.Is(err, storage.ErrDocumentExists) {
			writeError(w, http.StatusConflict, "document already exists")
			return
		}
		a.logger.Error("create document", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create document")
		return
	}
	doc, err := a.store.GetDocument(body.DocID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load created document")
		return
	}
	writeJSON(w, http.StatusCreated, toDocView(doc))
}

// GET /api/v1/docs
func (a *API) handleListDocs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := storage.DocumentFilter{
		Limit:  atoiDefault(q.Get("limit"), 0),
		Offset: atoiDefault(q.Get("offset"), 0),
	}
	if s := q.Get("updated_since"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			filter.UpdatedSince = t
		} else {
			writeError(w, http.StatusBadRequest, "updated_since must be RFC3339")
			return
		}
	}
	docs, err := a.store.ListDocuments(filter)
	if err != nil {
		a.logger.Error("list documents", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list documents")
		return
	}
	views := make([]docView, len(docs))
	for i := range docs {
		views[i] = toDocView(&docs[i])
	}
	writeJSON(w, http.StatusOK, map[string]any{"documents": views})
}

// GET /api/v1/docs/{id}
func (a *API) handleGetDoc(w http.ResponseWriter, r *http.Request) {
	doc, err := a.store.GetDocument(r.PathValue("id"))
	if err != nil {
		a.docError(w, err, "get document")
		return
	}
	writeJSON(w, http.StatusOK, toDocView(doc))
}

// DELETE /api/v1/docs/{id}
func (a *API) handleDeleteDoc(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.store.DeleteDocument(id); err != nil {
		a.docError(w, err, "delete document")
		return
	}
	a.engine.Forget(id)
	if a.emitter != nil {
		a.emitter.Emit("on_document_deleted", map[string]any{
			"doc_id":  id,
			"user_id": nil,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "doc_id": id})
}

// GET /api/v1/docs/{id}/history
func (a *API) handleHistory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := a.store.GetDocument(id); err != nil {
		a.docError(w, err, "history")
		return
	}
	after := int64(atoiDefault(r.URL.Query().Get("after"), 0))
	limit := atoiDefault(r.URL.Query().Get("limit"), 0)

	ops, err := a.store.GetOpsSince(id, after)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load history")
		return
	}
	if limit > 0 && len(ops) > limit {
		ops = ops[:limit]
	}
	type opView struct {
		Seq       int64     `json:"seq"`
		SessionID string    `json:"session_id"`
		Payload   []byte    `json:"payload"`
		CreatedAt time.Time `json:"created_at"`
	}
	views := make([]opView, len(ops))
	for i, op := range ops {
		views[i] = opView{Seq: op.Seq, SessionID: op.SessionID, Payload: op.Payload, CreatedAt: op.CreatedAt}
	}
	writeJSON(w, http.StatusOK, map[string]any{"doc_id": id, "ops": views})
}

// POST /api/v1/docs/{id}/snapshot
func (a *API) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := a.store.GetDocument(id); err != nil {
		a.docError(w, err, "snapshot")
		return
	}
	seq, err := a.engine.Snapshot(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to snapshot")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"doc_id": id, "seq": seq})
}

// GET /api/v1/docs/{id}/export
func (a *API) handleExport(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := a.store.GetDocument(id); err != nil {
		a.docError(w, err, "export")
		return
	}
	state, err := a.engine.Export(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to export")
		return
	}
	// state is the raw Automerge binary; JSON-encode it as base64.
	writeJSON(w, http.StatusOK, map[string]any{"doc_id": id, "state": state})
}

// docError maps storage errors to HTTP responses.
func (a *API) docError(w http.ResponseWriter, err error, op string) {
	if errors.Is(err, storage.ErrDocumentNotFound) {
		writeError(w, http.StatusNotFound, "document not found")
		return
	}
	a.logger.Error(op, "error", err)
	writeError(w, http.StatusInternalServerError, "internal error")
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}
