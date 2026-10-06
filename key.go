package taskgraph

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Key derives a canonical, collision-free dedup key from a task name
// and its parameters. Each segment is length-prefixed, so parameter
// boundaries can never blur: Key("t", "a", "bc") differs from
// Key("t", "ab", "c"), and Key("ab", "c") differs from Key("a", "bc").
//
// Parameters are encoded as JSON, which is deterministic for a given
// value (struct fields in declaration order, map keys sorted) and
// self-delimiting for strings. Values that cannot be marshaled fall
// back to their %#v representation.
func Key(name string, params ...interface{}) string {
	var b strings.Builder
	writeSegment(&b, []byte(name))
	for _, p := range params {
		data, err := json.Marshal(p)
		if err != nil {
			data = []byte(fmt.Sprintf("%#v", p))
		}
		writeSegment(&b, data)
	}
	return b.String()
}

func writeSegment(b *strings.Builder, data []byte) {
	b.WriteString(strconv.Itoa(len(data)))
	b.WriteByte(':')
	b.Write(data)
	b.WriteByte(';')
}
