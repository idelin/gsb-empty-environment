package netsec

import (
	"golang.org/x/net/idna"
)

func newIDNAProfile() *idna.Profile {
	return idna.New(
		idna.MapForLookup(),
		idna.Transitional(false),
		idna.BidiRule(),
		idna.VerifyDNSLength(true),
	)
}
