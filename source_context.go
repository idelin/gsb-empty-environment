package depseal

import "context"

type sourceContextKey struct{}

func contextWithSource(ctx context.Context, source Source) context.Context {
	return context.WithValue(ctx, sourceContextKey{}, source)
}

func sourceFromContext(ctx context.Context) (Source, bool) {
	source, ok := ctx.Value(sourceContextKey{}).(Source)
	return source, ok
}
