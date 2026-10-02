// demo 演示 tracevault 的完整流程：
// 批量写入轨迹 -> 日常聚合热力查询 -> 出示司法授权凭证定向还原 -> 审计日志校验。
package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"tracevault/tracevault"
)

func main() {
	// 1. 初始化：主密钥派生出假名化、网格盐、记录加密、审计四个子密钥。
	master := make([]byte, 32)
	if _, err := rand.Read(master); err != nil {
		panic(err)
	}
	keys := tracevault.DeriveKeys(master)
	vault := tracevault.NewVault(keys, tracevault.DefaultBucketSize, 0.01)

	// 2. 模拟海量写入：1000 名用户在上海人民广场附近随机游走，跨 3 个小时级时间桶。
	base := time.Now().Truncate(time.Hour).Unix() - 2*tracevault.DefaultBucketSize
	target := "user-0042"
	for u := 0; u < 1000; u++ {
		id := fmt.Sprintf("user-%04d", u)
		for step := 0; step < 12; step++ {
			ts := base + int64(step)*900 // 每 15 分钟一个点
			angle := float64(u*12+step) * 0.7
			lat := 31.2304 + 0.05*math.Sin(angle)*math.Cos(float64(step)/5)
			lng := 121.4737 + 0.05*math.Cos(angle)*math.Sin(float64(step)/5)
			if err := vault.Insert(id, lat, lng, ts); err != nil {
				panic(err)
			}
		}
	}
	fmt.Printf("[写入] 1000 名用户 x 12 个轨迹点 = 12000 条加密记录\n")

	// 3. 日常只读查询：只能拿到脱敏聚合热力分布（k=5 匿名抑制）。
	bucket := vault.BucketOf(base + 3600)
	heat := vault.Heatmap(bucket, 5)
	total := 0
	for _, n := range heat {
		total += n
	}
	fmt.Printf("[热力] 时间桶 %d：%d 个匿名网格，共 %d 次观测（无任何实体信息）\n", bucket, len(heat), total)

	// 4. 司法审计：监管方持审计密钥签发针对单个实体、有界时间段的授权凭证。
	from := vault.BucketOf(base)
	to := vault.BucketOf(base + 4*3600)
	cred, err := tracevault.IssueCredential(keys.AuditKey(), "沪检刑调[2026]第001号", target, from, to, time.Now().Add(time.Hour).Unix())
	if err != nil {
		panic(err)
	}
	fmt.Printf("[授权] 签发凭证：目标=%s 时间桶=[%d,%d]\n", cred.EntityID, cred.From, cred.To)

	// 5. 定向还原：仅解密目标实体在授权时间段内的轨迹。
	pts, err := vault.Reveal(cred)
	if err != nil {
		panic(err)
	}
	fmt.Printf("[还原] 解密出 %s 的 %d 个轨迹点：\n", target, len(pts))
	for _, pt := range pts[:3] {
		fmt.Printf("        %s  (%.6f, %.6f)\n", time.Unix(pt.Timestamp, 0).UTC().Format(time.RFC3339), pt.Lat, pt.Lng)
	}
	fmt.Printf("        ... 共 %d 点\n", len(pts))

	// 6. 审计日志：完整留痕，哈希链防篡改。
	entries := vault.AuditLog().Entries()
	out, _ := json.MarshalIndent(entries, "", "  ")
	fmt.Printf("[审计] 日志 %d 条，哈希链校验：", len(entries))
	if err := vault.AuditLog().Verify(); err != nil {
		fmt.Printf("失败 %v\n", err)
	} else {
		fmt.Println("通过")
	}
	fmt.Println(string(out))
}
