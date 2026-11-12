package netsec

import (
	"io"
)

func readLimited(reader io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		limit = 1 << 20
	}
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errorf(ErrInvalidRequestObject, "request object response is too large")
	}
	return data, nil
}
