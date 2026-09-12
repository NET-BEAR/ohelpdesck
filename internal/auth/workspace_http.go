package auth

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/NET-BEAR/ohelpdesck/internal/core"
	"github.com/NET-BEAR/ohelpdesck/internal/workspace"
	"github.com/google/uuid"
)

func (h *HTTPHandler) workspaceList(w http.ResponseWriter, r *http.Request) {
	if h.workspace == nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	p, _, ok := h.authenticate(w, r, false)
	if !ok || !h.allowed(w, r, p, PermissionConversationRead) {
		return
	}
	actor, e := uuid.Parse(p.UserID)
	if e != nil {
		writeError(w, http.StatusInternalServerError, "internal_error")
		return
	}
	q, e := parseWorkspaceList(r, actor)
	if e != nil {
		writeError(w, http.StatusBadRequest, "validation_failed")
		return
	}
	result, e := h.workspace.List(r.Context(), actor, q)
	h.writeWorkspaceError(w, result, e)
}
func parseWorkspaceList(r *http.Request, actor uuid.UUID) (workspace.ListQuery, error) {
	v := r.URL.Query()
	q := workspace.ListQuery{Cursor: v.Get("cursor")}
	if raw := v.Get("limit"); raw != "" {
		n, e := strconv.Atoi(raw)
		if e != nil {
			return q, e
		}
		q.Limit = n
		if q.Limit < 1 || q.Limit > 100 {
			return q, workspace.ErrInvalidQuery
		}
	}
	if sort := v.Get("sort"); sort != "" && sort != "number_desc" {
		return q, workspace.ErrInvalidQuery
	}
	for _, raw := range v["channel_id"] {
		id, e := uuid.Parse(raw)
		if e != nil {
			return q, e
		}
		q.ChannelIDs = append(q.ChannelIDs, id)
	}
	for _, raw := range v["status"] {
		q.Statuses = append(q.Statuses, core.ConversationStatus(raw))
	}
	for _, raw := range v["priority"] {
		q.Priorities = append(q.Priorities, core.ConversationPriority(raw))
	}
	if raw := v.Get("assignee"); raw != "" {
		switch raw {
		case "me":
			q.Assignee = &actor
		case "unassigned":
			q.Unassigned = true
		default:
			id, e := uuid.Parse(raw)
			if e != nil {
				return q, e
			}
			q.Assignee = &id
		}
	}
	return q, nil
}

func (h *HTTPHandler) workspaceRead(w http.ResponseWriter, r *http.Request) {
	if h.workspace == nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	p, _, ok := h.authenticate(w, r, false)
	if !ok || !h.allowed(w, r, p, PermissionConversationRead) {
		return
	}
	actor, e := uuid.Parse(p.UserID)
	if e != nil {
		writeError(w, http.StatusInternalServerError, "internal_error")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/conversations/")
	parts := strings.Split(path, "/")
	if len(parts) < 1 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	id, e := uuid.Parse(parts[0])
	if e != nil {
		writeError(w, http.StatusBadRequest, "validation_failed")
		return
	}
	perms, e := h.repository.EffectivePermissions(r.Context(), p)
	if e != nil {
		writeError(w, http.StatusInternalServerError, "internal_error")
		return
	}
	canReply := containsPermission(perms, PermissionConversationReply)
	canReassign := containsPermission(perms, PermissionConversationReassign)
	if len(parts) == 1 {
		d, e := h.workspace.Detail(r.Context(), actor, id, canReply, canReassign)
		h.writeWorkspaceError(w, d, e)
		return
	}
	if len(parts) == 2 && parts[1] == "messages" {
		v := r.URL.Query()
		if v.Get("before") != "" && v.Get("after") != "" {
			writeError(w, http.StatusBadRequest, "validation_failed")
			return
		}
		limit := 0
		if raw := v.Get("limit"); raw != "" {
			limit, e = strconv.Atoi(raw)
			if e != nil {
				writeError(w, http.StatusBadRequest, "validation_failed")
				return
			}
		}
		page, e := h.workspace.Messages(r.Context(), actor, id, v.Get("before"), v.Get("after"), limit)
		h.writeWorkspaceError(w, page, e)
		return
	}
	writeError(w, http.StatusNotFound, "not_found")
}
func containsPermission(all []Permission, want Permission) bool {
	for _, p := range all {
		if p == want {
			return true
		}
	}
	return false
}
func (h *HTTPHandler) writeWorkspaceError(w http.ResponseWriter, value any, err error) {
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, value)
	case errors.Is(err, workspace.ErrNotFound):
		writeError(w, http.StatusNotFound, "conversation_not_found")
	case errors.Is(err, workspace.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden")
	case errors.Is(err, workspace.ErrInvalidQuery):
		writeError(w, http.StatusBadRequest, "validation_failed")
	default:
		writeError(w, http.StatusInternalServerError, "internal_error")
	}
}
