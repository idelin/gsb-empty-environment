package splitter

// FaultAdapter wraps a BlobStore and lets tests inject errors or simulated
// crashes (by panicking from the hook) around each operation. Phase is
// "before" or "after"; an "after" hook error models a lost acknowledgement:
// the inner write already succeeded but the caller sees a failure.
type FaultAdapter struct {
	Inner BlobStore
	Hook  func(op, phase, name string) error
}

func (f *FaultAdapter) run(op, phase, name string) error {
	if f.Hook == nil {
		return nil
	}
	return f.Hook(op, phase, name)
}

// Put implements BlobStore.
func (f *FaultAdapter) Put(name string, data []byte) error {
	if err := f.run("put", "before", name); err != nil {
		return err
	}
	err := f.Inner.Put(name, data)
	if hookErr := f.run("put", "after", name); hookErr != nil {
		return hookErr
	}
	return err
}

// Get implements BlobStore.
func (f *FaultAdapter) Get(name string) ([]byte, error) {
	if err := f.run("get", "before", name); err != nil {
		return nil, err
	}
	data, err := f.Inner.Get(name)
	if hookErr := f.run("get", "after", name); hookErr != nil {
		return nil, hookErr
	}
	return data, err
}

// Delete implements BlobStore.
func (f *FaultAdapter) Delete(name string) error {
	if err := f.run("delete", "before", name); err != nil {
		return err
	}
	err := f.Inner.Delete(name)
	if hookErr := f.run("delete", "after", name); hookErr != nil {
		return hookErr
	}
	return err
}

// List implements BlobStore.
func (f *FaultAdapter) List() ([]string, error) {
	if err := f.run("list", "before", ""); err != nil {
		return nil, err
	}
	return f.Inner.List()
}
