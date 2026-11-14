package netsec

import "time"

// nowFunc is replaceable in tests without exporting a clock abstraction.
var nowFunc = time.Now
