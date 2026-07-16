package op

import (
	"net/http"

	"github.com/bestruirui/octopus/internal/apperror"
)

const (
	CodeProtocolRoutingValidation = "protocol_routing.validation_failed"
	CodeProtocolRoutingNotFound   = "protocol_routing.not_found"
	CodeProtocolRoutingConflict   = "protocol_routing.revision_conflict"
	CodeProtocolRoutingDatabase   = "protocol_routing.database_error"
)

func protocolRoutingValidationError(message string) *apperror.Error {
	return apperror.New(CodeProtocolRoutingValidation, message).WithStatus(http.StatusBadRequest)
}

func protocolRoutingNotFoundError(message string) *apperror.Error {
	return apperror.New(CodeProtocolRoutingNotFound, message).WithStatus(http.StatusNotFound)
}

func protocolRoutingConflictError(expected, current int64) *apperror.Error {
	return apperror.New(CodeProtocolRoutingConflict, "protocol policy revision is stale").
		WithStatus(http.StatusConflict).
		WithParams(map[string]any{"expected_revision": expected, "current_revision": current})
}

func protocolRoutingDatabaseError(message string, err error) *apperror.Error {
	return apperror.Wrap(CodeProtocolRoutingDatabase, message, err).WithStatus(http.StatusInternalServerError)
}
