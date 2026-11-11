package netsec

import "fmt"

// Route is the mutable publication state threaded through policy rules.
// Tag mutations rewrite Tags in place; Prefix and PathLen stay constant
// for a single evaluation run.
type Route struct {
	Prefix  Prefix
	PathLen int
	Tags    TagSet
}

func (r Route) String() string {
	return fmt.Sprintf("route{%s pathlen=%d %s}", r.Prefix, r.PathLen, r.Tags)
}
