package hotring

import "time"

// nowUnixNano is a variable so tests can stub the clock.
var nowUnixNano = func() int64 { return time.Now().UnixNano() }
