package session

// UpdateOption configures a policy update.
type UpdateOption func(*updateOptions)

type updateOptions struct {
	revoke []Tuple
}

// WithRevocation makes an update revoke the given active sessions in addition
// to changing the policy for new connections. Tuples without an active
// session are reported in UpdateResult.Missing rather than failing the update.
func WithRevocation(tuples ...Tuple) UpdateOption {
	return func(o *updateOptions) {
		o.revoke = append(o.revoke, tuples...)
	}
}
