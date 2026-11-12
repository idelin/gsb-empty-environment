package admission

import "context"

// Full gRPC method names of the Bazel Remote Execution / ByteStream APIs
// that mutate cache state. Embedders dispatch on these strings, exactly as
// grpc.UnaryServerInterceptor / grpc.StreamServerInterceptor see them.
const (
	MethodActionCacheUpdate   = "/build.bazel.remote.execution.v2.ActionCache/UpdateActionResult"
	MethodCASFindMissing      = "/build.bazel.remote.execution.v2.ContentAddressableStorage/FindMissingBlobs"
	MethodCASBatchUpdateBlobs = "/build.bazel.remote.execution.v2.ContentAddressableStorage/BatchUpdateBlobs"
	MethodByteStreamRead      = "/google.bytestream.ByteStream/Read"
	MethodByteStreamWrite     = "/google.bytestream.ByteStream/Write"
	MethodByteStreamQuery     = "/google.bytestream.ByteStream/QueryWriteStatus"
)

// ActionCacheUpdateRequest is the decoded UpdateActionResult request.
type ActionCacheUpdateRequest struct {
	ActionDigest Digest
	Result       *ActionResult
}

// CASBlobUpload is one entry of BatchUpdateBlobs (or a single CAS upload).
type CASBlobUpload struct {
	ResourceName string
	Data         []byte
}

// Front is the embeddable gRPC front-end. It contains no protobuf or
// grpc-go dependency: the embedding service converts its inbound messages
// into the request structs here and calls Intercept. Intercept performs
// admission and returns nil for anything that may proceed to the
// (untrusted) backend. Reads are always passed through.
type Front struct {
	Guard *Guard
	// DenyUnknown makes Intercept reject unrecognized method names instead
	// of passing them through. Deployments that enumerate every served
	// method should enable it as a fail-closed default.
	DenyUnknown bool
}

// NewFront builds a Front over the given guard.
func NewFront(guard *Guard) *Front {
	return &Front{Guard: guard}
}

// Intercept applies write admission for one gRPC call. The method is the
// full gRPC method name. req must be one of:
//
//   - ActionCacheUpdateRequest for UpdateActionResult
//   - []CASBlobUpload for BatchUpdateBlobs
//   - CASBlobUpload for a single CAS/ByteStream one-shot write
//
// ByteStream Write is streaming: use Guard.NewBlobWriter instead.
// Read-only methods (GetActionResult, FindMissingBlobs, ByteStream Read,
// QueryWriteStatus) and, by default, unknown methods return nil.
func (f *Front) Intercept(ctx context.Context, method string, req any) error {
	switch method {
	case MethodActionCacheUpdate:
		update, ok := req.(ActionCacheUpdateRequest)
		if !ok {
			return fail("front", ErrInvalidDigest, "unexpected request type %T for %s", req, method)
		}
		return f.Guard.AdmitActionResultWrite(ctx, update.ActionDigest, update.Result)

	case MethodCASBatchUpdateBlobs:
		uploads, ok := req.([]CASBlobUpload)
		if !ok {
			return fail("front", ErrInvalidDigest, "unexpected request type %T for %s", req, method)
		}
		for _, upload := range uploads {
			if _, err := f.Guard.AdmitCASWrite(ctx, upload.ResourceName, upload.Data); err != nil {
				return err
			}
		}
		return nil

	case MethodCASFindMissing, MethodByteStreamRead, MethodByteStreamQuery:
		// Read-only: always forwarded.
		return nil

	default:
		if f.DenyUnknown {
			return fail("front", ErrPermissionDenied, "unknown method %s is not registered with the admission front", method)
		}
		return nil
	}
}
