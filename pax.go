package safeunpack

import (
	"strconv"
	"strings"
)

func parsePAX(data []byte) (map[string]string, error) {
	records := make(map[string]string)
	for pos := 0; pos < len(data); {
		space := -1
		for i := pos; i < len(data); i++ {
			if data[i] == ' ' {
				space = i
				break
			}
			if data[i] < '0' || data[i] > '9' {
				return nil, wrapError(ErrMalformed, "", -1,
					"PAX record at byte %d has a non-decimal length", pos)
			}
		}
		if space < 0 {
			return nil, wrapError(ErrMalformed, "", -1,
				"PAX record at byte %d has no length separator", pos)
		}
		lengthText := data[pos:space]
		if len(lengthText) == 0 {
			return nil, wrapError(ErrMalformed, "", -1, "PAX record has an empty length")
		}
		n, err := strconv.ParseInt(string(lengthText), 10, 64)
		if err != nil || n <= 0 {
			return nil, wrapError(ErrMalformed, "", -1, "PAX record has an invalid length")
		}
		end := int64(pos) + n
		if end < int64(pos) || end > int64(len(data)) {
			return nil, wrapError(ErrMalformed, "", -1, "PAX record extends beyond header data")
		}
		recordEnd := int(end)
		if recordEnd <= space+3 || data[recordEnd-1] != '\n' {
			return nil, wrapError(ErrMalformed, "", -1, "PAX record has an invalid terminator")
		}
		body := data[space+1 : recordEnd-1]
		eq := -1
		for i, b := range body {
			if b == '=' {
				eq = i
				break
			}
		}
		if eq <= 0 {
			return nil, wrapError(ErrMalformed, "", -1, "PAX record has no keyword/value separator")
		}
		key := string(body[:eq])
		value := string(body[eq+1:])
		if strings.IndexByte(key, 0) >= 0 || strings.IndexByte(value, 0) >= 0 {
			return nil, wrapError(ErrMalformed, "", -1, "PAX record contains NUL")
		}
		records[key] = value
		pos = recordEnd
	}
	return records, nil
}

func parsePAXSize(raw string) (int64, error) {
	if raw == "" {
		return 0, wrapError(ErrMalformed, "", -1, "PAX size is empty")
	}
	for _, c := range raw {
		if c < '0' || c > '9' {
			return 0, wrapError(ErrMalformed, "", -1, "PAX size %q is not a non-negative decimal integer", raw)
		}
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return 0, wrapError(ErrMalformed, "", -1, "PAX size %q overflows or is negative", raw)
	}
	return n, nil
}

func mergePAX(dst map[string]string, src map[string]string) {
	for k, v := range src {
		dst[k] = v
	}
}
