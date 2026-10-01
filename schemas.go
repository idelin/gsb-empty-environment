package taggedunion

// Branch tags used by the demo registry and tests.
const (
	TagUser  uint16 = 1
	TagPoint uint16 = 2
	TagEmpty uint16 = 3 // legal empty branch: zero-length payload
)

// DemoRegistry builds a registry with two evolved versions of the User
// branch, one Point branch, and an empty branch. Version evolution is
// expressed only through explicit migration functions.
func DemoRegistry() *Registry {
	r := NewRegistry()

	r.Register(&Schema{Tag: TagUser, Version: 1, Fields: []Field{
		{Name: "name", Kind: KindString},
	}})
	r.Register(&Schema{Tag: TagUser, Version: 2, Fields: []Field{
		{Name: "name", Kind: KindString},
		{Name: "email", Kind: KindString},
	}})
	r.Register(&Schema{Tag: TagPoint, Version: 1, Fields: []Field{
		{Name: "x", Kind: KindInt32},
		{Name: "y", Kind: KindInt32},
	}})
	r.Register(&Schema{Tag: TagEmpty, Version: 1, Fields: nil})

	// Explicit evolution mappings, both directions.
	r.RegisterMigration(TagUser, 1, 2, func(f map[string]any) (map[string]any, error) {
		return map[string]any{"name": f["name"], "email": ""}, nil
	})
	r.RegisterMigration(TagUser, 2, 1, func(f map[string]any) (map[string]any, error) {
		return map[string]any{"name": f["name"]}, nil
	})
	return r
}
