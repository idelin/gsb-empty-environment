package gin_test

import (
	"fmt"

	gin "prescreen.local/ps-31f1927a0b"
)

func ExampleIndex_Query() {
	idx := gin.New(
		gin.WithPendingCapacity(128),
		gin.WithPostingTreeThreshold(8),
		gin.WithoutLossyPages(),
	)

	tid := gin.MakeTID(1, 1)
	if err := idx.InsertJSONB(tid, []byte(`{"tags":["go","gin"],"user":{"name":"alice"}}`)); err != nil {
		panic(err)
	}

	matches := idx.Query(gin.And("tags[]=go", "user.name=alice"))
	fmt.Printf("matches=%d tid=%d recheck=%v\n", len(matches), matches[0].TID, matches[0].Recheck)

	// Output:
	// matches=1 tid=65537 recheck=false
}
