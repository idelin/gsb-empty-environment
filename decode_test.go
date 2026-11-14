package netsec

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func sampleEntry() *CacheEntry {
	return &CacheEntry{
		Key:       "user:42",
		Value:     `{"name":"alice"}`,
		CreatedAt: 1_700_000_000,
		ExpiresAt: 1_700_003_600,
		Metadata: &CacheMetadata{
			Namespace: "users",
			Owner:     "cache-service",
			Revision:  3,
		},
		Tags: []string{"profile", "v1"},
	}
}

func mustEncode(t *testing.T, registry *Registry, value any) []byte {
	t.Helper()
	payload, err := registry.Encode(value)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	return payload
}

func mutateEnvelope(t *testing.T, payload []byte, mutate func(map[string]json.RawMessage)) []byte {
	t.Helper()
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	mutate(envelope)
	out, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return out
}

func mutateData(t *testing.T, payload []byte, mutate func(map[string]json.RawMessage)) []byte {
	t.Helper()
	return mutateEnvelope(t, payload, func(envelope map[string]json.RawMessage) {
		var data map[string]json.RawMessage
		if err := json.Unmarshal(envelope["data"], &data); err != nil {
			t.Fatalf("unmarshal data: %v", err)
		}
		mutate(data)
		encoded, err := json.Marshal(data)
		if err != nil {
			t.Fatalf("marshal data: %v", err)
		}
		envelope["data"] = encoded
	})
}

func setField(t *testing.T, object map[string]json.RawMessage, key string, value any) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal %q: %v", key, err)
	}
	object[key] = encoded
}

func TestDecodeValidRoundTrip(t *testing.T) {
	registry := NewDefaultRegistry()
	entry := sampleEntry()
	payload := mustEncode(t, registry, entry)

	decoded, err := registry.Decode(payload)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	got, ok := decoded.(*CacheEntry)
	if !ok {
		t.Fatalf("Decode() returned %T, want *CacheEntry", decoded)
	}
	if !reflect.DeepEqual(got, entry) {
		t.Fatalf("Decode() = %#v, want %#v", got, entry)
	}

	typed, err := DecodeAs[CacheEntry](registry, payload)
	if err != nil {
		t.Fatalf("DecodeAs() error = %v", err)
	}
	if !reflect.DeepEqual(typed, entry) {
		t.Fatalf("DecodeAs() = %#v, want %#v", typed, entry)
	}

	// Struct values are accepted by Encode as well as pointers.
	valuePayload := mustEncode(t, registry, *entry)
	valueDecoded, err := registry.Decode(valuePayload)
	if err != nil {
		t.Fatalf("Decode(struct value) error = %v", err)
	}
	if !reflect.DeepEqual(valueDecoded, entry) {
		t.Fatalf("Decode(struct value) = %#v, want %#v", valueDecoded, entry)
	}
}

func TestDecodeRejectsUnknownType(t *testing.T) {
	registry := NewDefaultRegistry()
	payload := mutateEnvelope(t, mustEncode(t, registry, sampleEntry()), func(envelope map[string]json.RawMessage) {
		setField(t, envelope, "type", "cache.entry.evil")
	})

	decoded, err := registry.Decode(payload)
	if !errors.Is(err, ErrUnknownType) {
		t.Fatalf("Decode() error = %v, want ErrUnknownType", err)
	}
	if decoded != nil {
		t.Fatalf("Decode() returned %T on unknown type", decoded)
	}
}

func TestDecodeRejectsVersionChange(t *testing.T) {
	registry := NewDefaultRegistry()
	payload := mutateEnvelope(t, mustEncode(t, registry, sampleEntry()), func(envelope map[string]json.RawMessage) {
		setField(t, envelope, "version", 2)
	})

	decoded, err := registry.Decode(payload)
	if !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("Decode() error = %v, want ErrVersionMismatch", err)
	}
	if decoded != nil {
		t.Fatalf("Decode() returned %T on version mismatch", decoded)
	}
}

func TestDecodeRejectsTypeConfusion(t *testing.T) {
	registry := NewDefaultRegistry()
	valid := mustEncode(t, registry, sampleEntry())

	t.Run("unknown root field", func(t *testing.T) {
		payload := mutateData(t, valid, func(data map[string]json.RawMessage) {
			setField(t, data, "admin", true)
		})
		if _, err := registry.Decode(payload); !errors.Is(err, ErrFieldNotAllowed) {
			t.Fatalf("Decode() error = %v, want ErrFieldNotAllowed", err)
		}
	})

	t.Run("forged nested type tag", func(t *testing.T) {
		payload := mutateData(t, valid, func(data map[string]json.RawMessage) {
			var metadata map[string]json.RawMessage
			if err := json.Unmarshal(data["metadata"], &metadata); err != nil {
				t.Fatal(err)
			}
			setField(t, metadata, "$type", "cache.entry")
			encoded, _ := json.Marshal(metadata)
			data["metadata"] = encoded
		})
		if _, err := registry.Decode(payload); !errors.Is(err, ErrFieldNotAllowed) {
			t.Fatalf("Decode() error = %v, want ErrFieldNotAllowed", err)
		}
	})

	t.Run("wrong nested JSON type", func(t *testing.T) {
		payload := mutateData(t, valid, func(data map[string]json.RawMessage) {
			var metadata map[string]json.RawMessage
			if err := json.Unmarshal(data["metadata"], &metadata); err != nil {
				t.Fatal(err)
			}
			setField(t, metadata, "namespace", 123)
			encoded, _ := json.Marshal(metadata)
			data["metadata"] = encoded
		})
		if _, err := registry.Decode(payload); !errors.Is(err, ErrInvalidEnvelope) {
			t.Fatalf("Decode() error = %v, want ErrInvalidEnvelope", err)
		}
	})

	t.Run("data is not an object", func(t *testing.T) {
		payload := mutateEnvelope(t, valid, func(envelope map[string]json.RawMessage) {
			setField(t, envelope, "data", []any{"cache.entry"})
		})
		if _, err := registry.Decode(payload); !errors.Is(err, ErrInvalidEnvelope) {
			t.Fatalf("Decode() error = %v, want ErrInvalidEnvelope", err)
		}
	})

	t.Run("type identifier is not a string", func(t *testing.T) {
		payload := mutateEnvelope(t, valid, func(envelope map[string]json.RawMessage) {
			setField(t, envelope, "type", map[string]any{"name": "cache.entry"})
		})
		if _, err := registry.Decode(payload); !errors.Is(err, ErrInvalidEnvelope) {
			t.Fatalf("Decode() error = %v, want ErrInvalidEnvelope", err)
		}
	})

	t.Run("unexpected envelope key", func(t *testing.T) {
		payload := mutateEnvelope(t, valid, func(envelope map[string]json.RawMessage) {
			setField(t, envelope, "fallback_type", "cache.entry")
		})
		if _, err := registry.Decode(payload); !errors.Is(err, ErrInvalidEnvelope) {
			t.Fatalf("Decode() error = %v, want ErrInvalidEnvelope", err)
		}
	})

	t.Run("null field", func(t *testing.T) {
		payload := mutateData(t, valid, func(data map[string]json.RawMessage) {
			data["key"] = json.RawMessage("null")
		})
		if _, err := registry.Decode(payload); !errors.Is(err, ErrInvalidEnvelope) {
			t.Fatalf("Decode() error = %v, want ErrInvalidEnvelope", err)
		}
	})

	t.Run("duplicate field", func(t *testing.T) {
		payload := []byte(`{"type":"cache.entry","version":1,"data":{"key":"a","key":"b","value":"v","created_at":1,"expires_at":2,"metadata":{"namespace":"n","revision":1}}}`)
		if _, err := registry.Decode(payload); !errors.Is(err, ErrDuplicateField) {
			t.Fatalf("Decode() error = %v, want ErrDuplicateField", err)
		}
	})
}

func TestDecodeRejectsExcessiveNesting(t *testing.T) {
	registry := NewDefaultRegistry(WithMaxDepth(6))
	valid := mustEncode(t, registry, sampleEntry())

	// A legal payload fits at this depth.
	if _, err := registry.Decode(valid); err != nil {
		t.Fatalf("Decode(valid) error = %v", err)
	}

	deep := strings.Repeat("[", 16) + `"x"` + strings.Repeat("]", 16)
	payload := mutateData(t, valid, func(data map[string]json.RawMessage) {
		data["tags"] = json.RawMessage(deep)
	})
	if _, err := registry.Decode(payload); !errors.Is(err, ErrMaxDepth) {
		t.Fatalf("Decode(deep) error = %v, want ErrMaxDepth", err)
	}

	defaultRegistry := NewDefaultRegistry()
	defaultDeep := strings.Repeat("[", DefaultMaxDepth+2) + `"x"` + strings.Repeat("]", DefaultMaxDepth+2)
	defaultPayload := mutateData(t, mustEncode(t, defaultRegistry, sampleEntry()), func(data map[string]json.RawMessage) {
		data["tags"] = json.RawMessage(defaultDeep)
	})
	if _, err := defaultRegistry.Decode(defaultPayload); !errors.Is(err, ErrMaxDepth) {
		t.Fatalf("Decode(default deep) error = %v, want ErrMaxDepth", err)
	}

	// Deeper than encoding/json's internal scanner ceiling: the byte-level
	// pre-scan must still classify this as ErrMaxDepth.
	extremeDeep := strings.Repeat("[", 12_000) + `"x"` + strings.Repeat("]", 12_000)
	extremePayload := []byte(`{"type":"cache.entry","version":1,"data":{` +
		`"key":"k","value":"v","created_at":1,"expires_at":2,` +
		`"metadata":{"namespace":"n","revision":1},"tags":` + extremeDeep + `}}`)
	if _, err := defaultRegistry.Decode(extremePayload); !errors.Is(err, ErrMaxDepth) {
		t.Fatalf("Decode(extreme deep) error = %v, want ErrMaxDepth", err)
	}
}

type nodeRecord struct {
	Name string      `json:"name"`
	Next *nodeRecord `json:"next"`
	Alt  *nodeRecord `json:"alt"`
}

func newNodeRegistry(t *testing.T) *Registry {
	t.Helper()
	registry := NewRegistry()
	err := RegisterType[nodeRecord](registry, "test.node", 1, []FieldSpec{
		{Name: "name", Required: true},
		{Name: "next"},
		{Name: "alt"},
	}, nil)
	if err != nil {
		t.Fatalf("RegisterType(nodeRecord) error = %v", err)
	}
	return registry
}

func TestDecodeRejectsReferenceCycles(t *testing.T) {
	registry := newNodeRegistry(t)

	cases := map[string]string{
		"self":      `{"type":"test.node","version":1,"data":{"name":"root","next":{"$ref":"#/data"}}}`,
		"indirect":  `{"type":"test.node","version":1,"data":{"name":"root","next":{"$ref":"#/data/alt"},"alt":{"name":"other","next":{"$ref":"#/data"}}}}`,
		"ref chain": `{"type":"test.node","version":1,"data":{"name":"root","next":{"$ref":"#/data/alt"},"alt":{"$ref":"#/data/alt"}}}`,
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			decoded, err := registry.Decode([]byte(payload))
			if !errors.Is(err, ErrCycle) {
				t.Fatalf("Decode() error = %v, want ErrCycle", err)
			}
			if decoded != nil {
				t.Fatalf("Decode() returned %T for cyclic payload", decoded)
			}
		})
	}
}

func TestDecodeResolvesAcyclicReferences(t *testing.T) {
	registry := newNodeRegistry(t)
	payload := []byte(`{
		"type":"test.node",
		"version":1,
		"data":{
			"name":"root",
			"next":{"$ref":"#/data/alt"},
			"alt":{"name":"shared","alt":{"name":"leaf"}}
		}
	}`)

	decoded, err := registry.Decode(payload)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	root := decoded.(*nodeRecord)
	if root.Name != "root" || root.Next == nil || root.Next.Name != "shared" {
		t.Fatalf("unexpected root: %#v", root)
	}
	if root.Alt == nil || root.Alt.Name != "shared" || root.Alt.Alt == nil || root.Alt.Alt.Name != "leaf" {
		t.Fatalf("unexpected alt: %#v", root.Alt)
	}
}

func TestDecodeRejectsMalformedReferences(t *testing.T) {
	registry := newNodeRegistry(t)
	cases := map[string]struct {
		payload []byte
		want    error
	}{
		"dangling": {
			[]byte(`{"type":"test.node","version":1,"data":{"name":"root","next":{"$ref":"#/data/missing"}}}`),
			ErrInvalidReference,
		},
		"outside data": {
			[]byte(`{"type":"test.node","version":1,"data":{"name":"root","next":{"$ref":"#/type"}}}`),
			ErrInvalidReference,
		},
		"extra ref fields": {
			[]byte(`{"type":"test.node","version":1,"data":{"name":"root","next":{"$ref":"#/data/alt","type":"cache.entry"}}}`),
			ErrInvalidReference,
		},
		"non-string ref": {
			[]byte(`{"type":"test.node","version":1,"data":{"name":"root","next":{"$ref":1}}}`),
			ErrInvalidReference,
		},
		"scalar target": {
			[]byte(`{"type":"test.node","version":1,"data":{"name":"root","next":{"$ref":"#/data/name"}}}`),
			ErrInvalidEnvelope,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := registry.Decode(tc.payload)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Decode() error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestDecodeAppliesRangeAndConsistencyValidation(t *testing.T) {
	registry := NewDefaultRegistry()
	valid := mustEncode(t, registry, sampleEntry())

	cases := []struct {
		name   string
		mutate func(map[string]json.RawMessage)
	}{
		{
			name: "created_at out of range",
			mutate: func(data map[string]json.RawMessage) {
				setField(t, data, "created_at", 0)
			},
		},
		{
			name: "key too long",
			mutate: func(data map[string]json.RawMessage) {
				setField(t, data, "key", strings.Repeat("k", maxCacheKeyLength+1))
			},
		},
		{
			name: "too many tags",
			mutate: func(data map[string]json.RawMessage) {
				tags := make([]string, maxCacheTags+1)
				for i := range tags {
					tags[i] = fmt.Sprintf("tag-%d", i)
				}
				setField(t, data, "tags", tags)
			},
		},
		{
			name: "expiry before creation",
			mutate: func(data map[string]json.RawMessage) {
				setField(t, data, "expires_at", 1_700_000_000)
			},
		},
		{
			name: "ttl too long",
			mutate: func(data map[string]json.RawMessage) {
				setField(t, data, "expires_at", 1_700_000_000+MaxCacheTTLSeconds+1)
			},
		},
		{
			name: "duplicate tag",
			mutate: func(data map[string]json.RawMessage) {
				setField(t, data, "tags", []string{"same", "same"})
			},
		},
		{
			name: "empty tag",
			mutate: func(data map[string]json.RawMessage) {
				setField(t, data, "tags", []string{""})
			},
		},
		{
			name: "nested revision out of range",
			mutate: func(data map[string]json.RawMessage) {
				var metadata map[string]json.RawMessage
				if err := json.Unmarshal(data["metadata"], &metadata); err != nil {
					t.Fatal(err)
				}
				setField(t, metadata, "revision", 0)
				encoded, _ := json.Marshal(metadata)
				data["metadata"] = encoded
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := mutateData(t, valid, tc.mutate)
			decoded, err := registry.Decode(payload)
			if !errors.Is(err, ErrValidation) {
				t.Fatalf("Decode() error = %v, want ErrValidation", err)
			}
			if decoded != nil {
				t.Fatalf("Decode() returned %T for invalid value", decoded)
			}
		})
	}
}

func TestDecodeRejectsMissingRequiredField(t *testing.T) {
	registry := NewDefaultRegistry()
	payload := mutateData(t, mustEncode(t, registry, sampleEntry()), func(data map[string]json.RawMessage) {
		delete(data, "metadata")
	})
	if _, err := registry.Decode(payload); !errors.Is(err, ErrMissingField) {
		t.Fatalf("Decode() error = %v, want ErrMissingField", err)
	}
}

func TestDecodeRejectsOversizedPayload(t *testing.T) {
	registry := NewDefaultRegistry(WithMaxBytes(64))
	_, err := registry.Decode(mustEncode(t, NewDefaultRegistry(), sampleEntry()))
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("Decode() error = %v, want ErrPayloadTooLarge", err)
	}
}

func TestDecodeRejectsMalformedEnvelope(t *testing.T) {
	registry := NewDefaultRegistry()
	valid := mustEncode(t, registry, sampleEntry())

	cases := map[string]struct {
		payload []byte
	}{
		"string version": {mutateEnvelope(t, valid, func(envelope map[string]json.RawMessage) {
			setField(t, envelope, "version", "1")
		})},
		"float version": {mutateEnvelope(t, valid, func(envelope map[string]json.RawMessage) {
			envelope["version"] = json.RawMessage("1.0")
		})},
		"negative version": {mutateEnvelope(t, valid, func(envelope map[string]json.RawMessage) {
			envelope["version"] = json.RawMessage("-1")
		})},
		"missing type": {mutateEnvelope(t, valid, func(envelope map[string]json.RawMessage) {
			delete(envelope, "type")
		})},
		"missing data": {mutateEnvelope(t, valid, func(envelope map[string]json.RawMessage) {
			delete(envelope, "data")
		})},
		"oversized type ID": {mutateEnvelope(t, valid, func(envelope map[string]json.RawMessage) {
			setField(t, envelope, "type", strings.Repeat("a", maxTypeIDLength+1))
		})},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := registry.Decode(tc.payload); !errors.Is(err, ErrInvalidEnvelope) {
				t.Fatalf("Decode() error = %v, want ErrInvalidEnvelope", err)
			}
		})
	}
}

type unregisteredInner struct {
	Name string `json:"name"`
}

type outerWithUnregistered struct {
	Inner *unregisteredInner `json:"inner"`
}

func TestDecodeRejectsUnregisteredNestedType(t *testing.T) {
	registry := NewRegistry()
	if err := RegisterType[outerWithUnregistered](registry, "test.outer", 1, []FieldSpec{{Name: "inner"}}, nil); err != nil {
		t.Fatalf("RegisterType(outer) error = %v", err)
	}
	payload := []byte(`{"type":"test.outer","version":1,"data":{"inner":{"name":"x"}}}`)
	_, err := registry.Decode(payload)
	if !errors.Is(err, ErrUnregisteredFieldType) {
		t.Fatalf("Decode() error = %v, want ErrUnregisteredFieldType", err)
	}
}

func TestZeroValueRegistry(t *testing.T) {
	var registry Registry
	if err := RegisterType[auditRecord](&registry, "test.zero", 1, []FieldSpec{
		{Name: "id", Required: true},
		{Name: "name", Required: true},
	}, nil); err != nil {
		t.Fatalf("RegisterType() error = %v", err)
	}
	payload := []byte(`{"type":"test.zero","version":1,"data":{"id":3,"name":"ok"}}`)
	decoded, err := registry.Decode(payload)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if decoded.(*auditRecord).ID != 3 {
		t.Fatalf("Decode() = %#v", decoded)
	}
}

type auditRecord struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

func TestOnlyAllowlistedTypesAreConstructed(t *testing.T) {
	var factoryCalls int32
	registry := NewRegistry()
	err := registry.Register(TypeSpec{
		ID:      "test.audit",
		Version: 7,
		New: func() any {
			atomic.AddInt32(&factoryCalls, 1)
			return &auditRecord{}
		},
		Fields: []FieldSpec{
			{Name: "id", Required: true, IntRange: &IntRange{Min: 1, Max: 100}},
			{Name: "name", Required: true, LenRange: &LenRange{Min: 1, Max: 16}},
		},
		Validate: func(value any) error {
			record := value.(*auditRecord)
			if record.Name == "forbidden" {
				return fmt.Errorf("forbidden name")
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	atomic.StoreInt32(&factoryCalls, 0)

	valid := []byte(`{"type":"test.audit","version":7,"data":{"id":1,"name":"ok"}}`)
	decoded, err := registry.Decode(valid)
	if err != nil {
		t.Fatalf("Decode(valid) error = %v", err)
	}
	if _, ok := decoded.(*auditRecord); !ok {
		t.Fatalf("Decode() returned %T, want *auditRecord", decoded)
	}
	if got := atomic.LoadInt32(&factoryCalls); got != 1 {
		t.Fatalf("factory calls after valid decode = %d, want 1", got)
	}

	invalid := map[string]struct {
		payload      []byte
		factoryCalls int32
	}{
		"unknown type":   {[]byte(`{"type":"test.unknown","version":7,"data":{"id":1,"name":"ok"}}`), 0},
		"wrong version":  {[]byte(`{"type":"test.audit","version":8,"data":{"id":1,"name":"ok"}}`), 0},
		"unknown field":  {[]byte(`{"type":"test.audit","version":7,"data":{"id":1,"name":"ok","payload":{}}}`), 0},
		"deep payload":   {[]byte(`{"type":"test.audit","version":7,"data":{"id":1,"name":"ok","x":` + strings.Repeat("[", 40) + strings.Repeat("]", 40) + `}}`), 0},
		"cyclic payload": {[]byte(`{"type":"test.audit","version":7,"data":{"id":{"$ref":"#/data"},"name":"ok"}}`), 0},
		"bad range":      {[]byte(`{"type":"test.audit","version":7,"data":{"id":101,"name":"ok"}}`), 1},
	}
	for name, tc := range invalid {
		t.Run(name, func(t *testing.T) {
			atomic.StoreInt32(&factoryCalls, 0)
			decoded, err := registry.Decode(tc.payload)
			if err == nil {
				t.Fatalf("Decode() succeeded with %T", decoded)
			}
			if got := atomic.LoadInt32(&factoryCalls); got != tc.factoryCalls {
				t.Fatalf("factory calls = %d for rejected payload, want %d", got, tc.factoryCalls)
			}
		})
	}
}

type dangerousValue string

func (*dangerousValue) UnmarshalJSON([]byte) error {
	panic("dangerous UnmarshalJSON must never run")
}

type unsafeRecord struct {
	Data map[string]any `json:"data"`
}

type unsafeCustomRecord struct {
	Value dangerousValue `json:"value"`
}

func TestRegistrationRejectsUnsafeFieldTypes(t *testing.T) {
	if err := RegisterType[unsafeRecord](NewRegistry(), "test.map", 1, []FieldSpec{{Name: "data"}}, nil); !errors.Is(err, ErrUnsupportedFieldType) {
		t.Fatalf("RegisterType(map) error = %v, want ErrUnsupportedFieldType", err)
	}
	if err := RegisterType[unsafeCustomRecord](NewRegistry(), "test.custom", 1, []FieldSpec{{Name: "value"}}, nil); !errors.Is(err, ErrUnsupportedFieldType) {
		t.Fatalf("RegisterType(custom unmarshaler) error = %v, want ErrUnsupportedFieldType", err)
	}
}

type guardedRecord struct {
	Name string `json:"name"`
}

var guardedUnmarshalCalls int32

func (*guardedRecord) UnmarshalJSON([]byte) error {
	atomic.AddInt32(&guardedUnmarshalCalls, 1)
	return errors.New("dangerous method invoked")
}

func TestRegisteredTypeUnmarshalJSONIsNeverInvoked(t *testing.T) {
	atomic.StoreInt32(&guardedUnmarshalCalls, 0)
	registry := NewRegistry()
	if err := RegisterType[guardedRecord](registry, "test.guarded", 1, []FieldSpec{{Name: "name", Required: true}}, nil); err != nil {
		t.Fatalf("RegisterType() error = %v", err)
	}

	payload := []byte(`{"type":"test.guarded","version":1,"data":{"name":"safe"}}`)
	decoded, err := registry.Decode(payload)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if decoded.(*guardedRecord).Name != "safe" {
		t.Fatalf("Decode() = %#v", decoded)
	}
	if got := atomic.LoadInt32(&guardedUnmarshalCalls); got != 0 {
		t.Fatalf("UnmarshalJSON called %d times", got)
	}
}

func TestRegistrationRejectsDuplicatesAndBadSpecs(t *testing.T) {
	registry := NewRegistry()
	spec := TypeSpec{
		ID:      "test.duplicate",
		Version: 1,
		New:     func() any { return &auditRecord{} },
		Fields:  []FieldSpec{{Name: "id"}},
	}
	if err := registry.Register(spec); err != nil {
		t.Fatalf("first Register() error = %v", err)
	}
	if err := registry.Register(spec); !errors.Is(err, ErrDuplicateType) {
		t.Fatalf("duplicate Register() error = %v, want ErrDuplicateType", err)
	}
	if err := registry.Register(TypeSpec{
		ID:      "test.roomal",
		Version: 1,
		New:     func() any { return &auditRecord{} },
		Fields:  []FieldSpec{{Name: "id"}},
	}); !errors.Is(err, ErrDuplicateType) {
		t.Fatalf("duplicate Go type Register() error = %v, want ErrDuplicateType", err)
	}
	if err := NewRegistry().Register(TypeSpec{
		ID:      "bad id",
		Version: 1,
		New:     func() any { return &auditRecord{} },
	}); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("bad type ID Register() error = %v, want ErrInvalidSpec", err)
	}
	if err := NewRegistry().Register(TypeSpec{
		ID:      "test.nofield",
		Version: 1,
		New:     func() any { return &auditRecord{} },
		Fields:  []FieldSpec{{Name: "missing"}},
	}); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("missing field Register() error = %v, want ErrInvalidSpec", err)
	}
}

func FuzzDecodeNeverPanics(f *testing.F) {
	registry := NewDefaultRegistry()
	nodeRegistry := NewRegistry()
	if err := RegisterType[nodeRecord](nodeRegistry, "test.node", 1, []FieldSpec{
		{Name: "name", Required: true},
		{Name: "next"},
		{Name: "alt"},
	}, nil); err != nil {
		f.Fatalf("RegisterType(nodeRecord) error = %v", err)
	}

	seeds := [][]byte{
		nil,
		[]byte(`{}`),
		[]byte(`null`),
		[]byte(`{"type":"cache.entry","version":1,"data":{}}`),
		[]byte(`{"type":"cache.entry","version":1,"data":{"key":"k","value":"v","created_at":1,"expires_at":2,"metadata":{"namespace":"n","revision":1}}}`),
		[]byte(`{"type":"test.node","version":1,"data":{"name":"root","next":{"$ref":"#/data"}}}`),
		[]byte(`{"type":"cache.entry","version":1,"data":{"key":` + strings.Repeat("[", 100) + `}}`),
	}
	for _, seed := range seeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, payload []byte) {
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Fatalf("Decode panicked for %q: %v", payload, recovered)
			}
		}()
		_, _ = registry.Decode(payload)
		_, _ = nodeRegistry.Decode(payload)
	})
}
