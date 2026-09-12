package auth

import (
	"errors"
	"net/http"
	"strings"

	"github.com/NET-BEAR/ohelpdesck/internal/core"
	"github.com/google/uuid"
)

type outboundMessageResponse struct {
	ID             uuid.UUID          `json:"id"`
	ConversationID uuid.UUID          `json:"conversation_id"`
	ChannelID      uuid.UUID          `json:"channel_id"`
	Status         core.MessageStatus `json:"status"`
	Duplicate      bool               `json:"duplicate"`
}

func (h *HTTPHandler) queueOutbound(w http.ResponseWriter, r *http.Request) {
	if h.outbound == nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	principal, _, ok := h.authenticate(w, r, true)
	if !ok || !h.allowed(w, r, principal, PermissionConversationReply) {
		return
	}
	segments := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/conversations/"), "/")
	if len(segments) != 2 || segments[0] == "" || segments[1] != "messages" {
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
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		writeError(w, http.StatusBadRequest, "validation_failed")
		return
	}
	var input struct {
		Text             string     `json:"text"`
		HTML             string     `json:"html"`
		ReplyToMessageID *uuid.UUID `json:"reply_to_message_id"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	message, err := h.outbound.QueueOutbound(r.Context(), core.QueueOutboundCommand{ConversationID: conversationID, ActorID: actorID, Text: input.Text, HTML: input.HTML, ReplyToMessageID: input.ReplyToMessageID, IdempotencyKey: key})
	switch {
	case err == nil:
		status := http.StatusCreated
		if message.Duplicate {
			status = http.StatusOK
		}
		writeJSON(w, status, outboundMessageResponse{ID: message.ID, ConversationID: message.ConversationID, ChannelID: message.ChannelID, Status: message.Status, Duplicate: message.Duplicate})
	case errors.Is(err, core.ErrConversationNotFound):
		writeError(w, http.StatusNotFound, "conversation_not_found")
	case errors.Is(err, core.ErrConversationForbidden):
		writeError(w, http.StatusForbidden, "forbidden")
	case errors.Is(err, core.ErrIdempotencyConflict):
		writeError(w, http.StatusConflict, "idempotency_conflict")
	case errors.Is(err, core.ErrInvalidOutbound):
		writeError(w, http.StatusBadRequest, "validation_failed")
	default:
		writeError(w, http.StatusInternalServerError, "internal_error")
	}
}
