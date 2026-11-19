package sopsmeta

import (
	"crypto/hmac"
	"crypto/sha512"
	"encoding/base64"
	"fmt"
	"sort"
	"strconv"
)

// ComputeMAC derives the expected SOPS MAC for a decrypted file: an
// HMAC-SHA512, keyed with the recovered data key, over a canonical
// encoding of every value in the file plus the metadata's lastmodified
// timestamp. The result is base64-encoded, matching the sops .mac field.
func ComputeMAC(data map[string]any, lastModified string, dataKey []byte) string {
	mac := hmac.New(sha512.New, dataKey)
	writeValue(mac, data)
	mac.Write([]byte("lastmodified:" + lastModified))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// VerifyMAC recomputes the MAC over all decrypted values and compares it
// with the MAC stored in the metadata, in constant time. It must be called
// after decryption, before the plaintext is used. A metadata block without
// a MAC is rejected.
func VerifyMAC(meta Metadata, data map[string]any, dataKey []byte) error {
	if meta.MAC == "" {
		return ErrMissingMAC
	}
	stored, err := base64.StdEncoding.DecodeString(meta.MAC)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrMalformedMAC, err)
	}
	expected, err := base64.StdEncoding.DecodeString(ComputeMAC(data, meta.LastModified, dataKey))
	if err != nil {
		return err
	}
	if !hmac.Equal(stored, expected) {
		return ErrMACMismatch
	}
	return nil
}

// hashWriter is the subset of hash.Hash used by writeValue.
type hashWriter interface {
	Write(p []byte) (int, error)
}

// writeValue writes a canonical, order-independent encoding of v to w.
// Map keys are sorted; every scalar is tagged with its type so that, for
// example, the string "1" and the number 1 hash differently.
func writeValue(w hashWriter, v any) {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			w.Write([]byte("map:key:" + k))
			writeValue(w, t[k])
		}
	case []any:
		for _, item := range t {
			writeValue(w, item)
		}
	case string:
		w.Write([]byte("str:" + t))
	case bool:
		w.Write([]byte("bool:" + strconv.FormatBool(t)))
	case nil:
		w.Write([]byte("nil:"))
	case float64:
		w.Write([]byte("float:" + strconv.FormatFloat(t, 'g', -1, 64)))
	case int:
		w.Write([]byte("int:" + strconv.Itoa(t)))
	case int64:
		w.Write([]byte("int:" + strconv.FormatInt(t, 10)))
	default:
		w.Write([]byte(fmt.Sprintf("unknown:%T:%v", t, t)))
	}
}
