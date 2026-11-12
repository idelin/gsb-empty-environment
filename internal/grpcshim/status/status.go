package status

import (
	"fmt"

	"google.golang.org/grpc/codes"
)

type Status struct {
	code    codes.Code
	message string
}

func New(c codes.Code, msg string) *Status {
	return &Status{code: c, message: msg}
}

func (s *Status) Code() codes.Code {
	return s.code
}

func (s *Status) Message() string {
	return s.message
}

func (s *Status) Error() string {
	return fmt.Sprintf("rpc error: code = %s desc = %s", s.code, s.message)
}

type statusError struct {
	status *Status
}

func (e *statusError) Error() string {
	return e.status.Error()
}

func (e *statusError) GRPCStatus() *Status {
	return e.status
}

func Error(c codes.Code, msg string) error {
	return &statusError{status: New(c, msg)}
}

func Errorf(c codes.Code, format string, args ...any) error {
	return Error(c, fmt.Sprintf(format, args...))
}

func FromError(err error) (*Status, bool) {
	if err == nil {
		return New(codes.OK, ""), true
	}
	if withStatus, ok := err.(interface{ GRPCStatus() *Status }); ok {
		return withStatus.GRPCStatus(), true
	}
	return New(codes.Unknown, err.Error()), false
}

func Code(err error) codes.Code {
	if err == nil {
		return codes.OK
	}
	s, _ := FromError(err)
	return s.Code()
}

func Convert(err error) *Status {
	s, _ := FromError(err)
	return s
}
