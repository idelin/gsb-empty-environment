package snaprepl

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// KV is the replicated state machine: a tiny key/value store.
// Commands have the form "set <key> <value>".
type KV struct {
	m map[string]string
}

func NewKV() *KV { return &KV{m: map[string]string{}} }

func (kv *KV) Apply(cmd string) {
	f := strings.Fields(cmd)
	if len(f) == 3 && f[0] == "set" {
		kv.m[f[1]] = f[2]
	}
}

func (kv *KV) Get(key string) (string, bool) {
	v, ok := kv.m[key]
	return v, ok
}

// Encode serializes the full state (used as snapshot content).
func (kv *KV) Encode() []byte {
	b, err := json.Marshal(kv.m)
	if err != nil {
		panic(err)
	}
	return b
}

// Restore replaces the whole state with the decoded snapshot content.
func (kv *KV) Restore(b []byte) error {
	m := map[string]string{}
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	kv.m = m
	return nil
}

func (kv *KV) Equal(o *KV) bool {
	if len(kv.m) != len(o.m) {
		return false
	}
	for k, v := range kv.m {
		if ov, ok := o.m[k]; !ok || ov != v {
			return false
		}
	}
	return true
}

func (kv *KV) String() string {
	keys := make([]string, 0, len(kv.m))
	for k := range kv.m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", k, kv.m[k]))
	}
	return "{" + strings.Join(parts, " ") + "}"
}
