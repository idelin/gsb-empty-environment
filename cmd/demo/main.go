// demo 演示自适应局部感知分片网关的完整工作流：
// 北京客户端就近路由华北 → 华北全宕平滑重选华南 → 跨国流量拦截降级。
package main

import (
	"errors"
	"fmt"
	"time"

	"shardgw"
)

var (
	locBeijing   = shardgw.Location{Country: "CN", Region: "华北", City: "北京", Lat: 39.9042, Lng: 116.4074}
	locShenzhen  = shardgw.Location{Country: "CN", Region: "华南", City: "深圳", Lat: 22.5431, Lng: 114.0579}
	locSingapore = shardgw.Location{Country: "SG", Region: "海外", City: "新加坡", Lat: 1.3521, Lng: 103.8198}
	locNewYork   = shardgw.Location{Country: "US", Region: "海外", City: "纽约", Lat: 40.7128, Lng: -74.0060}
)

func main() {
	geo := shardgw.NewGeoDB()
	must := func(err error) {
		if err != nil {
			panic(err)
		}
	}
	must(geo.Add("111.192.0.0/12", locBeijing))
	must(geo.Add("14.16.0.0/12", locShenzhen))
	must(geo.Add("8.8.8.0/24", locNewYork))

	north := shardgw.NewCluster("华北", locBeijing)
	south := shardgw.NewCluster("华南", locShenzhen)
	oversea := shardgw.NewCluster("海外", locSingapore)

	var northNodes []*shardgw.Node
	addNodes := func(c *shardgw.Cluster, loc shardgw.Location, subnet int, ids ...string) {
		for i, id := range ids {
			n := &shardgw.Node{ID: id, Addr: fmt.Sprintf("10.0.%d.%d:8080", subnet, i+1), Loc: loc}
			n.SetHealthy(true)
			c.AddNode(n)
			if c == north {
				northNodes = append(northNodes, n)
			}
		}
	}
	addNodes(north, locBeijing, 1, "north-1", "north-2", "north-3")
	addNodes(south, locShenzhen, 2, "south-1", "south-2", "south-3")
	addNodes(oversea, locSingapore, 3, "oversea-1", "oversea-2")

	gw := shardgw.NewGateway(geo, "CN", north, south, oversea)

	route := func(label, ip string) {
		d, err := gw.Route(ip)
		if err != nil {
			fmt.Printf("%-24s %v\n", label, err)
			return
		}
		fmt.Printf("%-24s %s\n", label, d)
	}

	beijingClient := "111.195.1.10"

	fmt.Println("== 1. 客户端就近路由（同城/同地域优先） ==")
	route("北京客户端", beijingClient)
	route("深圳客户端", "14.17.22.1")

	fmt.Println()
	fmt.Println("== 2. 华北集群全部宕机，平滑重选华南 ==")
	for _, n := range northNodes {
		n.SetHealthy(false)
	}
	route("北京客户端(华北全宕)", beijingClient)
	for _, n := range northNodes {
		n.SetHealthy(true)
	}
	route("北京客户端(华北恢复)", beijingClient)

	fmt.Println()
	fmt.Println("== 3. 跨国流量精准拦截降级 ==")
	_, err := gw.Route("8.8.8.8")
	fmt.Printf("%-24s %v (degraded=%v)\n", "美国客户端", err, errors.Is(err, shardgw.ErrCrossBorder))

	fmt.Println()
	fmt.Println("== 4. 路由决策时延（秒级识别要求） ==")
	start := time.Now()
	const n = 100000
	for i := 0; i < n; i++ {
		if _, err := gw.Route(fmt.Sprintf("111.195.%d.%d", (i/256)%256, i%256)); err != nil {
			panic(err)
		}
	}
	fmt.Printf("100000 次路由决策总耗时 %s，平均 %s/次\n", time.Since(start).Round(time.Microsecond), time.Since(start)/n)
}
