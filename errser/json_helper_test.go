package errser

import "encoding/json"

func jsonDecode(data []byte, v any) error {
	return json.Unmarshal(data, v)
}
