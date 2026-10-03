// demo 演示自适应局部感知分片网关的完整流程：
// 北京客户端 -> 华北集群；华北全宕机 -> 平滑重选华南；海外客户端 -> 拦截降级。
package main

import (
	"fmt"
	"net"
	"time"

	"geogw"
)

func main() {
	nodes := []*gateway.Node{
		{ID: "cn-north-1", Cluster: "华北", Region: "华北", City: "北京", Country: "CN",
			Lat: 39.9042, Lng: 116.4074, CIDRs: []string{"111.192.0.0/12"}},
		{ID: "cn-north-2", Cluster: "华北", Region: "华北", City: "北京", Country: "CN",
			Lat: 39.9042, Lng: 116.4074, CIDRs: []string{"111.192.0.0/12"}},
		{ID: "cn-south-1", Cluster: "华南", Region: "华南", City: "深圳", Country: "CN",
			Lat: 22.5431, Lng: 114.0579, CIDRs: []string{"113.108.0.0/16"}},
		{ID: "cn-south-2", Cluster: "华南", Region: "华南", City: "深圳", Country: "CN",
			Lat: 22.5431, Lng: 114.0579, CIDRs: []string{"113.108.0.0/16"}},
		{ID: "overseas-1", Cluster: "海外", Region: "美西", City: "圣何塞", Country: "US",
			Lat: 37.3382, Lng: -121.8863, CIDRs: []string{"8.8.8.0/24"}},
	}

	// 秒级健康检查：真实环境可替换为 TCP/HTTP 探测。
	prober := func(n *gateway.Node) (time.Duration, bool) {
		rtt := 2 * time.Millisecond // 演示：同城实测 2ms，跨区实测 25ms
		if n.Cluster != "华北" {
			rtt = 25 * time.Millisecond
		}
		return rtt, n.Up()
	}
	r := gateway.NewRouter(nodes, gateway.WithProber(prober, time.Second))
	defer r.Close()

	beijing := net.ParseIP("111.196.80.15")

	fmt.Println("== 1. 北京客户端正常路由（期望：华北集群） ==")
	for _, key := range []string{"order-1001", "order-1002", "order-1003"} {
		d, err := r.Route(key, beijing)
		fmt.Println("  ", d, errString(err))
	}

	fmt.Println("\n== 2. 华北集群全部宕机，秒级重选（期望：华南集群） ==")
	for _, n := range nodes {
		if n.Cluster == "华北" {
			n.SetHealth(false)
		}
	}
	time.Sleep(1200 * time.Millisecond) // 等待一个探测周期
	for _, key := range []string{"order-1001", "order-1002", "order-1003"} {
		d, err := r.Route(key, beijing)
		fmt.Println("  ", d, errString(err))
	}

	fmt.Println("\n== 3. 海外客户端访问（期望：跨国拦截降级） ==")
	d, err := r.Route("order-2001", net.ParseIP("8.8.8.8"))
	fmt.Println("  ", d, errString(err))
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return "| " + err.Error()
}
