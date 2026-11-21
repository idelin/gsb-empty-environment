package keyusage

// Key models a managed key with its declared purposes. Derived keys keep a
// pointer to their parent so that inheritance can be re-checked at call time.
type Key struct {
	ID        string
	Algorithm Algorithm
	Purposes  Purpose
	Parent    *Key
}

// IsDerived reports whether the key was derived from another key.
func (k *Key) IsDerived() bool {
	return k.Parent != nil
}
