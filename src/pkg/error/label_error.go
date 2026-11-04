package error

// Fork-specific errors of the labels API (see FORK_CHANGES.md).
var (
	ErrLabelNotFound = notFoundError("label not found")
)
