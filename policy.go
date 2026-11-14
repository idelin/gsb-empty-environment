package netsec

import "time"

// Policy describes the multi-SCT requirements enforced for one generation
// of the log trust list.
type Policy struct {
	// Generation must match the LogList generation it is applied with.
	Generation uint64
	// MinDistinctLogs is the minimum number of distinct trusted logs whose
	// SCTs must accompany a certificate.
	MinDistinctLogs int
	// MaxAge is the maximum permitted age of an SCT timestamp relative to
	// validation time. Zero disables the age check.
	MaxAge time.Duration
}
