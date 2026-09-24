package canonkey

import "encoding/json"

// jsonNumber aliases json.Number so canon.go can switch on it while users
// may pass either json.Number or plain Go numerics.
type jsonNumber = json.Number
