package netsec

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

func primaryKeyFromTuple(s *schema, tuple Row) (Key, error) {
	key := make(Key, len(s.primary))
	for _, id := range s.primary {
		value, exists := tuple[id]
		if !exists {
			return nil, fmt.Errorf("%w: primary-key column %d is absent", ErrInvalidTuple, id)
		}
		if IsUnchanged(value) {
			return nil, fmt.Errorf("%w: primary-key column %d cannot be UNCHANGED", ErrInvalidTuple, id)
		}
		key[id] = value
	}
	return key, nil
}

func normalizeKey(s *schema, key Key) (Key, error) {
	if len(key) == 0 {
		return nil, fmt.Errorf("%w: old key is empty", ErrInvalidTuple)
	}
	out := make(Key, len(s.primary))
	for _, id := range s.primary {
		value, exists := key[id]
		if !exists {
			return nil, fmt.Errorf("%w: old key is missing column %d", ErrInvalidTuple, id)
		}
		if IsUnchanged(value) {
			return nil, fmt.Errorf("%w: old key column %d is UNCHANGED", ErrInvalidTuple, id)
		}
		out[id] = value
	}
	for id := range key {
		if !s.isPrimary(id) {
			return nil, fmt.Errorf("%w: old key contains non-key column %d", ErrInvalidTuple, id)
		}
	}
	return out, nil
}

func keyString(key Key) string {
	if len(key) == 0 {
		return "{}"
	}
	ids := make([]int, 0, len(key))
	for id := range key {
		ids = append(ids, int(id))
	}
	sort.Ints(ids)
	var builder strings.Builder
	builder.WriteByte('{')
	for i, rawID := range ids {
		if i > 0 {
			builder.WriteByte(',')
		}
		id := ColumnID(rawID)
		builder.WriteString(strconv.Itoa(int(id)))
		builder.WriteByte('=')
		builder.WriteString(encodeKeyValue(key[id]))
	}
	builder.WriteByte('}')
	return builder.String()
}

func encodeKeyValue(value any) string {
	switch value := value.(type) {
	case nil:
		return "null"
	case string:
		return "s:" + strconv.Quote(value)
	case []byte:
		return "b:" + hex.EncodeToString(value)
	case bool:
		return "o:" + strconv.FormatBool(value)
	case int:
		return "i:" + strconv.FormatInt(int64(value), 10)
	case int8:
		return "i8:" + strconv.FormatInt(int64(value), 10)
	case int16:
		return "i16:" + strconv.FormatInt(int64(value), 10)
	case int32:
		return "i32:" + strconv.FormatInt(int64(value), 10)
	case int64:
		return "i64:" + strconv.FormatInt(value, 10)
	case uint:
		return "u:" + strconv.FormatUint(uint64(value), 10)
	case uint8:
		return "u8:" + strconv.FormatUint(uint64(value), 10)
	case uint16:
		return "u16:" + strconv.FormatUint(uint64(value), 10)
	case uint32:
		return "u32:" + strconv.FormatUint(uint64(value), 10)
	case uint64:
		return "u64:" + strconv.FormatUint(value, 10)
	case float32:
		return "f32:" + strconv.FormatUint(uint64(math.Float32bits(value)), 16)
	case float64:
		return "f64:" + strconv.FormatUint(math.Float64bits(value), 16)
	case time.Time:
		return "t:" + value.UTC().Format(time.RFC3339Nano)
	}

	// Primary keys are normally scalar database values. Keep the fallback
	// deterministic and type-tagged rather than relying on pointer addresses.
	encoded, err := json.Marshal(value)
	if err == nil {
		return "j:" + reflect.TypeOf(value).String() + ":" + string(encoded)
	}
	return "v:" + reflect.TypeOf(value).String() + ":" + fmt.Sprintf("%#v", value)
}
