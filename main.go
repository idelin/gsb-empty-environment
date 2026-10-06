package main

import (
	"fmt"
	"io/ioutil"
	"os"
)

func status(ok bool) string {
	if ok {
		return "OK  "
	}
	return "FAIL"
}

func main() {
	fmt.Println("聚合器故障演练：检查点=累计值+待发结果，发布与确认分离，恢复重发同 ID")
	fmt.Println("----------------------------------------------------------------------")
	fmt.Printf("%-22s %-30s %s\n", "故障点", "计算状态恢复", "效果确认")
	allOK := true
	for _, fp := range allFaultPoints {
		dir, err := ioutil.TempDir("", "agg-scenario")
		if err != nil {
			panic(err)
		}
		r := runScenario(dir, fp)
		os.RemoveAll(dir)
		compute := fmt.Sprintf("%s offset=%d/%d sum=%d",
			status(r.ComputeOK), r.FinalOffset, len(testEvents), r.FinalSum)
		effect := fmt.Sprintf("%s pending=%d total=%d/%d 投递=%d 重复=%d(已去重) 空洞=%d",
			status(r.EffectOK), r.PendingLeft, r.ReceiverTotal, r.ExpectedSum,
			r.Deliveries, r.Duplicates, r.GapErrors)
		fmt.Printf("%-22s %-30s %s\n", faultPointNames[fp], compute, effect)
		if !r.ComputeOK || !r.EffectOK {
			allOK = false
		}
	}
	fmt.Println("----------------------------------------------------------------------")
	if allOK {
		fmt.Println("全部故障点：计算状态恢复完整，效果确认精确一次（重复投递均被幂等去重）")
	} else {
		fmt.Println("存在未通过项")
		os.Exit(1)
	}
}
