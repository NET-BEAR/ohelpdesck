package auth

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/NET-BEAR/ohelpdesck/internal/core"
	"github.com/google/uuid"
)

type conversationResponse struct {
	ID           uuid.UUID                 `json:"id"`
	ChannelID    uuid.UUID                 `json:"channel_id"`
	Status       core.ConversationStatus   `json:"status"`
	Priority     core.ConversationPriority `json:"priority"`
	ResolvedAt   *time.Time                `json:"resolved_at,omitempty"`
	SnoozedUntil *time.Time                `json:"snoozed_until,omitempty"`
	Version      int64                     `json:"version"`
	Assignee     *conversationAssignee     `json:"assignee"`
}
type conversationAssignee struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (h *HTTPHandler) conversationCommand(w http.ResponseWriter, r *http.Request) {
	if h.conversations == nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	principal, _, ok := h.authenticate(w, r, true)
	if !ok {
		return
	}
	segments := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/conversations/"), "/")
	if len(segments) != 2 || segments[0] == "" {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	conversationID, err := uuid.Parse(segments[0])
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_failed")
		return
	}
	actorID, err := uuid.Parse(principal.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error")
		return
	}
	switch segments[1] {
	case "assignee":
		if !h.allowed(w, r, principal, PermissionConversationReassign) {
			return
		}
		var input struct {
			ExpectedVersion int64      `json:"expected_version"`
			AssigneeID      *uuid.UUID `json:"assignee_id"`
		}
		if !decodeJSON(w, r, &input) {
			return
		}
		if input.ExpectedVersion < 1 {
			writeError(w, http.StatusBadRequest, "validation_failed")
			return
		}
		conversation, err := h.conversations.Assign(r.Context(), core.AssignConversation{ConversationID: conversationID, ExpectedVersion: input.ExpectedVersion, AssigneeID: input.AssigneeID, ActorID: actorID})
		h.writeConversationResult(w, r, conversation, err)
	case "status":
		if !h.allowed(w, r, principal, PermissionConversationReply) {
			return
		}
		var input struct {
			ExpectedVersion int64                   `json:"expected_version"`
			Status          core.ConversationStatus `json:"status"`
			SnoozedUntil    *time.Time              `json:"snoozed_until"`
		}
		if !decodeJSON(w, r, &input) {
			return
		}
		if input.ExpectedVersion < 1 || !isConversationStatus(input.Status) {
			writeError(w, http.StatusBadRequest, "validation_failed")
			return
		}
		conversation, err := h.conversations.ChangeStatus(r.Context(), core.ChangeConversationStatus{ConversationID: conversationID, ExpectedVersion: input.ExpectedVersion, Status: input.Status, SnoozedUntil: input.SnoozedUntil, ActorID: actorID})
		h.writeConversationResult(w, r, conversation, err)
	case "priority":
		if !h.allowed(w, r, principal, PermissionConversationReply) {
			return
		}
		var input struct {
			ExpectedVersion int64                     `json:"expected_version"`
			Priority        core.ConversationPriority `json:"priority"`
		}
		if !decodeJSON(w, r, &input) {
			return
		}
		if input.ExpectedVersion < 1 || !isConversationPriority(input.Priority) {
			writeError(w, http.StatusBadRequest, "validation_failed")
			return
		}
		conversation, err := h.conversations.SetPriority(r.Context(), core.SetConversationPriority{ConversationID: conversationID, ExpectedVersion: input.ExpectedVersion, Priority: input.Priority, ActorID: actorID})
		h.writeConversationResult(w, r, conversation, err)
	default:
		writeError(w, http.StatusNotFound, "not_found")
	}
}

func (h *HTTPHandler) writeConversationResult(w http.ResponseWriter, r *http.Request, conversation core.Conversation, err error) {
	switch {
	case err == nil:
		var assignee *conversationAssignee
		if conversation.AssigneeID != nil {
			user, lookupErr := h.repository.ByID(r.Context(), conversation.AssigneeID.String())
			if lookupErr != nil {
				writeError(w, http.StatusInternalServerError, "internal_error")
				return
			}
			assignee = &conversationAssignee{ID: user.ID, Name: user.Name}
		}
		writeJSON(w, http.StatusOK, conversationResponse{ID: conversation.ID, ChannelID: conversation.ChannelID, Status: conversation.Status, Priority: conversation.Priority, ResolvedAt: conversation.ResolvedAt, SnoozedUntil: conversation.SnoozedUntil, Version: conversation.Version, Assignee: assignee})
	case errors.Is(err, core.ErrConversationNotFound):
		writeError(w, http.StatusNotFound, "conversation_not_found")
	case errors.Is(err, core.ErrConversationForbidden):
		writeError(w, http.StatusForbidden, "forbidden")
	case errors.Is(err, core.ErrVersionConflict):
		writeError(w, http.StatusConflict, "version_conflict")
	case errors.Is(err, core.ErrInvalidConversationTransition):
		writeError(w, http.StatusConflict, "invalid_transition")
	case errors.Is(err, core.ErrInvalidSnoozeDeadline), errors.Is(err, core.ErrInvalidConversationPriority), errors.Is(err, core.ErrAssigneeIneligible):
		writeError(w, http.StatusBadRequest, "validation_failed")
	default:
		writeError(w, http.StatusInternalServerError, "internal_error")
	}
}

func isConversationStatus(status core.ConversationStatus) bool {
	return status == core.ConversationOpen || status == core.ConversationPending || status == core.ConversationResolved || status == core.ConversationSnoozed
}

func isConversationPriority(priority core.ConversationPriority) bool {
	return priority == core.ConversationLow || priority == core.ConversationNormal || priority == core.ConversationHigh || priority == core.ConversationUrgent
}
