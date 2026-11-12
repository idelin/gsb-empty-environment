package netsec

import "encoding/base64"

var base64Encoding = base64.StdEncoding

func base64Std(s string) string {
	return base64Encoding.EncodeToString([]byte(s))
}
