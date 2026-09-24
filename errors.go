package canonicalkey

import "errors"

var (
	ErrInvalidRequest    = errors.New("invalid request")
	ErrInvalidValue      = errors.New("invalid value")
	ErrInvalidFieldType  = errors.New("invalid field type")
	ErrInvalidGraph      = errors.New("invalid graph")
	ErrMissingConfig     = errors.New("missing config_version")
	ErrNullConfig        = errors.New("null config_version is not permitted")
	ErrEmptyConfig       = errors.New("empty config_version is not permitted")
	ErrUnknownField      = errors.New("unknown request field")
	ErrDuplicateField    = errors.New("duplicate field")
	ErrDanglingReference = errors.New("dangling graph reference")
	ErrBudgetExceeded    = errors.New("resource budget exceeded")
	ErrDepthExceeded     = errors.New("depth budget exceeded")
	ErrCatalogClosed     = errors.New("catalog is closed")
	ErrInvalidUnicode    = errors.New("invalid Unicode string")
)
