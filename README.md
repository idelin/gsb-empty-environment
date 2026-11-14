# 轨迹匿名发布组件

出行数据发布时移除了姓名，攻击者仍可通过常去地点、时间规律和稀有路径关联公开信息，重新识别用户。本模块提供可直接导入 Go 数据发布服务的轨迹匿名组件。

## 处理流程

1. 删除 `UserID`、`DirectIdentifiers` 等直接标识符，并使它们不参与 JSON 序列化。
2. 按时间排序、去重并按 `MaxSamplePoints` / `MinSampleInterval` 采样。
3. 使用 `SpatialCellMeters` 空间栅格泛化坐标，使用 `TimeBucket` 时间桶泛化时间。
4. 在栅格单元内部做有界随机扰动，在时间桶内部做可选抖动，保证不改变派生的准标识符。
5. 抑制低支持度地点、低支持度泛化路径和低支持度规律作息。
6. 对用户属性做稀有值泛化，并检查每个准标识符组合是否满足 k-匿名。
7. 在发布前计算最坏/平均重识别概率、唯一轨迹率、稀有地点/轨迹/规律模式率，并执行风险门禁。

完整准标识符组合包含泛化路径和泛化时间序列；空间扰动按泛化单元、时间抖动按时间桶保持一致，因此同一等价类中的发布轨迹除伪名记录标识外不可区分。默认 `Seed` 仅用于可复现性，生产发布应使用不可公开且按批次轮换的盐值。

## 快速使用

```go
package main

import (
	"errors"
	"log"
	"time"

	netsec "example.org/netsec/q0492"
)

func main() {
	cfg := netsec.Config{
		K:                  5,
		SpatialCellMeters:  500,
		TimeBucket:         15 * time.Minute,
		MaxSamplePoints:    32,
		PerturbationMeters: 50,
		MaxRisk:            0.2,
	}
	anonymizer, err := netsec.NewAnonymizer(cfg)
	if err != nil {
		log.Fatal(err)
	}

	start := time.Now().Add(-time.Hour)
	end := time.Now()
	dataset := netsec.Dataset{
		{
			ID:     "trip-001",
			UserID: "zhang-san", // 不会出现在输出，也不会被 JSON 序列化
			Points: []netsec.Point{
				{Time: start, Lat: 31.23, Lon: 121.47},
				{Time: end, Lat: 31.25, Lon: 121.50},
			},
			QuasiIdentifiers: map[string]string{"age_band": "30-39"},
		},
	}

	result, err := anonymizer.Publish(dataset)
	if err != nil {
		var blocked *netsec.PublicationError
		if errors.As(err, &blocked) {
			log.Printf("risk=%+v", blocked.Risk)
		}
		log.Fatal(err)
	}
	published := result.Records // 仅包含泛化、扰动、k-匿名后的轨迹
	_ = published
}
```

## 主要 API

- `NewAnonymizer(Config) (*Anonymizer, error)`：校验并规范化配置。
- `(*Anonymizer).Anonymize(Dataset) (*Result, error)`：执行完整匿名流程，返回风险报告。
- `(*Anonymizer).Publish(Dataset) (*Result, error)`：匿名并执行发布门禁；不满足策略时返回 `*PublicationError`。
- `Anonymize(Dataset, Config)` / `Publish(Dataset, Config)`：包级便捷函数。
- `AssessRisk(Dataset, Config) (RiskReport, error)`：发布前独立风险评估。
- `ValidateKAnonymity(Dataset, Config) (KAnonymityReport, error)`：检查全部准标识符组合。

内置轨迹准标识符包括 `frequent_location_1`、`frequent_location_2`、`start_time_of_day`、`day_pattern`、`generalized_path` 和 `temporal_shape`。可通过 `Config.QICombinations` 指定额外组合；未指定时默认检查单属性、两两组合和完整组合，且完整组合始终会被检查。

## 配置要点

- `K`：最小等价类大小，必须至少为 2。
- `MinLocationSupport`：地点支持度阈值，默认等于 `K`。
- `MinTrajectorySupport`：泛化路径支持度阈值，默认等于 `K`；设为 1 可关闭稀有路径抑制。
- `MinPatternSupport` / `RegularRepeatDays`：规律作息的支持度阈值和重复天数。
- `MaxRisk`：允许的最大重识别概率与综合风险分数；零值按 `1/K` 计算。
- `PerturbationMeters` / `TemporalJitter`：零值表示关闭对应扰动；`DefaultConfig` 默认开启小范围扰动。
- `Seed`：使采样、抑制和扰动结果可复现；发布服务应使用不可公开的盐值。

## 验证

测试覆盖常去地点关联、时间规律、稀有地点、稀有轨迹、规律模式、准标识符组合 k-匿名、合法发布、风险阻断、确定性及输入不可变性。

```bash
mkdir -p .tmp
TMPDIR="$PWD/.tmp" go test ./...
```
