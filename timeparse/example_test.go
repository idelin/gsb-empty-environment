package timeparse_test

import (
	"fmt"
	"time"

	"example.org/domainreissue/q068/timeparse"
)

// 宿主典型接入方式：传入页面文本、抓取时刻、IANA 时区与站点回看窗口。
func ExampleParse() {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	fetchedAt := time.Date(2011, time.January, 2, 10, 0, 0, 0, loc)

	result, err := timeparse.Parse("发布于昨日 23:00", fetchedAt, timeparse.Rules{
		Location: loc,
		Lookback: 30 * 24 * time.Hour,
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	fmt.Println("ambiguous:", result.Ambiguous)
	fmt.Println("utc:", result.First().Format(time.RFC3339))
	for _, frag := range result.Fragments {
		fmt.Printf("fragment: %s %q\n", frag.Kind, frag.Text)
	}
	// Output:
	// ambiguous: false
	// utc: 2011-01-01T15:00:00Z
	// fragment: relative "昨日"
	// fragment: time "23:00"
}
