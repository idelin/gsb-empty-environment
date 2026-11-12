package netsec

import (
	"sync"
)

// ckBool encodes a PKCS#11 CK_BBOOL value for templates and objects.
func ckBool(value bool) []byte {
	if value {
		return []byte{1}
	}
	return []byte{0}
}

// memoryHSM is an in-process Backend used to exercise the guard. It keeps
// separate per-session object maps so session objects disappear when the
// session closes, exactly like a real token.
type memoryHSM struct {
	mu       sync.Mutex
	sessions map[SessionHandle]map[ObjectHandle]map[AttributeType][]byte
	token    map[ObjectHandle]map[AttributeType][]byte
	nextID   ObjectHandle

	wrapCalls  int
	copyCalls  int
	setCalls   int
	backendErr error
}

func newMemoryHSM() *memoryHSM {
	return &memoryHSM{
		sessions: make(map[SessionHandle]map[ObjectHandle]map[AttributeType][]byte),
		token:    make(map[ObjectHandle]map[AttributeType][]byte),
	}
}

func (h *memoryHSM) OpenSession() SessionHandle {
	h.mu.Lock()
	defer h.mu.Unlock()

	session := SessionHandle(len(h.sessions) + 1)
	h.sessions[session] = make(map[ObjectHandle]map[AttributeType][]byte)
	return session
}

func (h *memoryHSM) CloseSession(session SessionHandle) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.sessions, session)
}

// CreateObject stores an object. CKA_TOKEN=true selects token storage;
// every other object is scoped to the given session.
func (h *memoryHSM) CreateObject(session SessionHandle, template []Attribute) ObjectHandle {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.nextID++
	handle := h.nextID
	object := make(map[AttributeType][]byte)
	for _, attr := range template {
		object[attr.Type] = attr.Value
	}

	if isTokenObject(object) {
		h.token[handle] = object
	} else {
		h.sessions[session][handle] = object
	}
	return handle
}

func (h *memoryHSM) lookup(session SessionHandle, object ObjectHandle) (map[AttributeType][]byte, bool) {
	if attrs, ok := h.token[object]; ok {
		return attrs, true
	}
	sessionObjects, ok := h.sessions[session]
	if !ok {
		return nil, false
	}
	attrs, ok := sessionObjects[object]
	return attrs, ok
}

func (h *memoryHSM) GetAttributeValue(session SessionHandle, object ObjectHandle, attrs []AttributeType) ([]Attribute, error) {
	if h.backendErr != nil {
		return nil, h.backendErr
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	objectAttrs, ok := h.lookup(session, object)
	if !ok {
		return nil, ErrObjectHandleInvalid
	}

	result := make([]Attribute, 0, len(attrs))
	for _, attr := range attrs {
		if value, ok := objectAttrs[attr]; ok {
			result = append(result, Attribute{Type: attr, Value: value})
		}
	}
	return result, nil
}

func (h *memoryHSM) WrapKey(session SessionHandle, wrappingKey ObjectHandle, key ObjectHandle, mechanism Mechanism) ([]byte, error) {
	if h.backendErr != nil {
		return nil, h.backendErr
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := h.lookup(session, key); !ok {
		return nil, ErrObjectHandleInvalid
	}
	h.wrapCalls++
	return []byte("wrapped-key-bytes"), nil
}

func (h *memoryHSM) CopyObject(session SessionHandle, object ObjectHandle, template []Attribute) (ObjectHandle, error) {
	if h.backendErr != nil {
		return 0, h.backendErr
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	source, ok := h.lookup(session, object)
	if !ok {
		return 0, ErrObjectHandleInvalid
	}

	h.nextID++
	handle := h.nextID
	copyAttrs := make(map[AttributeType][]byte, len(source)+len(template))
	for attr, value := range source {
		copyAttrs[attr] = value
	}
	for _, attr := range template {
		copyAttrs[attr.Type] = attr.Value
	}

	if isTokenObject(copyAttrs) {
		h.token[handle] = copyAttrs
	} else {
		h.sessions[session][handle] = copyAttrs
	}
	h.copyCalls++
	return handle, nil
}

func (h *memoryHSM) SetAttributeValue(session SessionHandle, object ObjectHandle, template []Attribute) error {
	if h.backendErr != nil {
		return h.backendErr
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	objectAttrs, ok := h.lookup(session, object)
	if !ok {
		return ErrObjectHandleInvalid
	}
	for _, attr := range template {
		objectAttrs[attr.Type] = attr.Value
	}
	h.setCalls++
	return nil
}

func isTokenObject(attrs map[AttributeType][]byte) bool {
	value, ok := attrs[CKA_TOKEN]
	return ok && len(value) == 1 && value[0] == 1
}

// errorBackend always fails, used to verify fail-closed behavior.
type errorBackend struct {
	err error
}

func (b *errorBackend) GetAttributeValue(SessionHandle, ObjectHandle, []AttributeType) ([]Attribute, error) {
	return nil, b.err
}

func (b *errorBackend) WrapKey(SessionHandle, ObjectHandle, ObjectHandle, Mechanism) ([]byte, error) {
	return nil, b.err
}

func (b *errorBackend) CopyObject(SessionHandle, ObjectHandle, []Attribute) (ObjectHandle, error) {
	return 0, b.err
}

func (b *errorBackend) SetAttributeValue(SessionHandle, ObjectHandle, []Attribute) error {
	return b.err
}
