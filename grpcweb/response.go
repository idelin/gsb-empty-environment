package grpcweb

import (
	"errors"
	"net/http"
)

// ErrMissingTrailer means a gRPC-Web response had no FlagTrailer frame.
var ErrMissingTrailer = errors.New("grpcweb: response is missing the trailer frame")

// ErrStatusMismatch means a trailer frame grpc-status disagrees with the
// grpc-status carried in the real HTTP/2 trailers.
var ErrStatusMismatch = errors.New("grpcweb: forged grpc-status in trailer frame")

// BuildWebResponse assembles a complete gRPC-Web response for a proxy.
//
// dataMessages are each wrapped in a FlagData frame and the HTTP/2 trailers
// are serialized into one trailing FlagTrailer frame. grpc-status is taken
// only from trailers, and any trailer containing control characters (such
// as an injected CRLF) or a reserved "grpc-" custom key is rejected before
// bytes reach the client.
func BuildWebResponse(dataMessages [][]byte, trailers http.Header) ([]byte, error) {
	trailerFrame, err := MarshalTrailerFrame(trailers)
	if err != nil {
		return nil, err
	}

	size := len(trailerFrame)
	for _, msg := range dataMessages {
		size += FrameHeaderSize + len(msg)
	}

	out := make([]byte, 0, size)
	for _, msg := range dataMessages {
		out = append(out, MarshalDataFrame(msg)...)
	}
	out = append(out, trailerFrame...)
	return out, nil
}

// ParsedResponse is a decoded gRPC-Web response.
type ParsedResponse struct {
	// Data holds the concatenated FlagData frame payloads in stream order.
	Data []byte

	// Trailer is the validated final FlagTrailer frame.
	Trailer TrailerBlock
}

// ParseWebResponse decodes and validates a complete gRPC-Web response body.
// It rejects truncated or ordered-incorrect frames, missing trailers,
// malformed trailer lines, control-character injection, reserved metadata
// names, and any block that lacks grpc-status.
func ParseWebResponse(body []byte) (ParsedResponse, error) {
	frames, err := SplitFrames(body)
	if err != nil {
		return ParsedResponse{}, err
	}
	if len(frames) == 0 || frames[len(frames)-1].Flag != FlagTrailer {
		return ParsedResponse{}, ErrMissingTrailer
	}

	trailer, err := ParseTrailerBlock(frames[len(frames)-1].Payload)
	if err != nil {
		return ParsedResponse{}, err
	}
	if !trailer.HasStatus {
		return ParsedResponse{}, ErrMissingStatus
	}

	dataFrames := frames[:len(frames)-1]
	dataSize := 0
	for _, frame := range dataFrames {
		dataSize += len(frame.Payload)
	}
	data := make([]byte, 0, dataSize)
	for _, frame := range dataFrames {
		data = append(data, frame.Payload...)
	}

	return ParsedResponse{Data: data, Trailer: trailer}, nil
}
