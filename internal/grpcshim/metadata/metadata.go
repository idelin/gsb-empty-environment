package metadata

import (
	"context"
	"strings"
)

// MD is the metadata map carried by gRPC. It matches the real gRPC type.
type MD map[string][]string

func New(m ...string) MD {
	md := make(MD, len(m)/2)
	for i := 0; i < len(m); i += 2 {
		key := m[i]
		value := ""
		if i+1 < len(m) {
			value = m[i+1]
		}
		md[key] = append(md[key], value)
	}
	return md
}

func Pairs(kv ...string) MD {
	return New(kv...)
}

func Join(mds ...MD) MD {
	out := MD{}
	for _, md := range mds {
		for key, values := range md {
			out[key] = append(out[key], values...)
		}
	}
	return out
}

func NewIncomingContext(ctx context.Context, md MD) context.Context {
	return context.WithValue(ctx, incomingKey{}, md)
}

func NewOutgoingContext(ctx context.Context, md MD) context.Context {
	return context.WithValue(ctx, outgoingKey{}, md)
}

func AppendToOutgoingContext(ctx context.Context, kv ...string) context.Context {
	md, _ := FromOutgoingContext(ctx)
	md = md.Copy()
	for i := 0; i < len(kv); i += 2 {
		value := ""
		if i+1 < len(kv) {
			value = kv[i+1]
		}
		md[kv[i]] = append(md[kv[i]], value)
	}
	return NewOutgoingContext(ctx, md)
}

func FromIncomingContext(ctx context.Context) (MD, bool) {
	md, ok := ctx.Value(incomingKey{}).(MD)
	return md, ok
}

func FromOutgoingContext(ctx context.Context) (MD, bool) {
	md, ok := ctx.Value(outgoingKey{}).(MD)
	return md, ok
}

func (md MD) Copy() MD {
	out := make(MD, len(md))
	for key, values := range md {
		out[key] = append([]string(nil), values...)
	}
	return out
}

func (md MD) Get(key string) []string {
	return md[strings.ToLower(key)]
}

func (md MD) Set(key string, values ...string) {
	md[strings.ToLower(key)] = append([]string(nil), values...)
}

func (md MD) Append(key string, values ...string) {
	md[strings.ToLower(key)] = append(md[strings.ToLower(key)], values...)
}

func (md MD) Delete(key string) {
	delete(md, strings.ToLower(key))
}

func (md MD) Len() int {
	return len(md)
}

type incomingKey struct{}
type outgoingKey struct{}
