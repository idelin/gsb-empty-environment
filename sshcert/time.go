package sshcert

import "time"

// unixNow returns the current time as UNIX seconds. Isolated so the lifetime
// logic stays injectable through VerifyOptions.Now.
func unixNow() int64 {
	return time.Now().Unix()
}
