package negcache

import "context"

// Source fetches a value from the authoritative store. It returns the value
// and its source version on success, or an error classifiable by Classifier.
// Injectable: tests and the demo plug in scripted failures without any
// remote cache or network dependency.
type Source func(ctx context.Context, tenant, key string) (value []byte, version uint64, err error)
