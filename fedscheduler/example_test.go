package fedscheduler_test

import (
	"errors"
	"fmt"

	"prescreen.local/ps-4cf6c31a4a/fedscheduler"
)

func ExampleAllocate() {
	clusters := []fedscheduler.Cluster{
		{Name: "cluster-a", Weight: 5, Capacity: 2},
		{Name: "cluster-b", Weight: 3, Capacity: 10},
		{Name: "cluster-c", Weight: 2, Capacity: 10},
	}
	result, err := fedscheduler.Allocate(20, clusters)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(result.Replicas["cluster-a"], result.Replicas["cluster-b"], result.Replicas["cluster-c"])
	fmt.Println("saturated:", result.Saturated)
	// Output:
	// 2 10 8
	// saturated: [cluster-a cluster-b]
}

func ExampleAllocateWithSpread_minGroups() {
	clusters := []fedscheduler.Cluster{
		{Name: "a1", Region: "cn-east", Weight: 1, Capacity: 10},
		{Name: "b1", Region: "cn-north", Weight: 1, Capacity: 10},
		{Name: "c1", Region: "cn-south", Weight: 1, Capacity: 10},
	}
	constraint := fedscheduler.SpreadConstraint{Mode: fedscheduler.SpreadMinGroups, MinGroups: 3}
	result, err := fedscheduler.AllocateWithSpread(4, clusters, []fedscheduler.SpreadConstraint{constraint})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(result.Replicas)
	// Output:
	// map[a1:2 b1:1 c1:1]
}

func ExampleRebalance_keepExisting() {
	clusters := []fedscheduler.Cluster{
		{Name: "cluster-a", Weight: 1, Capacity: 3},
		{Name: "cluster-b", Weight: 1, Capacity: 100},
	}
	existing := map[string]int64{"cluster-a": 7, "cluster-b": 3}
	result, err := fedscheduler.Rebalance(10, clusters, existing, fedscheduler.KeepExisting)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("plan:", result.Replicas)
	fmt.Println("migrated:", result.Migrated)
	// Output:
	// plan: map[cluster-a:3 cluster-b:7]
	// migrated: 4
}

func ExampleAllocate_insufficientCapacity() {
	clusters := []fedscheduler.Cluster{
		{Name: "cluster-a", Weight: 1, Capacity: 3},
		{Name: "cluster-b", Weight: 1, Capacity: 2},
	}
	_, err := fedscheduler.Allocate(10, clusters)
	var ice *fedscheduler.InsufficientCapacityError
	if errors.As(err, &ice) {
		fmt.Println("deficit:", ice.Deficit)
		fmt.Println("max plan:", ice.MaxPlan)
	}
	// Output:
	// deficit: 5
	// max plan: map[cluster-a:3 cluster-b:2]
}
