package mtuinfer

import "testing"

func bruteForce(t *testing.T, in Input) (int, int) {
	t.Helper()
	prep, err := prepare(in)
	if err != nil {
		t.Fatalf("prepare returned error: %v", err)
	}
	keys := append([]PathKey(nil), prep.activeKeys...)
	sortKeys(keys)

	linkIDs := make([]string, 0, len(prep.linkMin))
	for id := range prep.linkMin {
		linkIDs = append(linkIDs, id)
	}
	sortStrings(linkIDs)

	guaranteed := int(^uint(0) >> 1)
	possible := -1
	mtu := make(map[string]int, len(linkIDs))

	var enumerateMTUs func(int)
	enumerateMTUs = func(index int) {
		if index == len(linkIDs) {
			enumerateRoutes(prep, keys, mtu, &guaranteed, &possible)
			return
		}
		id := linkIDs[index]
		for value := prep.linkMin[id]; value <= prep.linkMax[id]; value++ {
			mtu[id] = value
			enumerateMTUs(index + 1)
		}
	}
	enumerateMTUs(0)
	return guaranteed, possible
}

func enumerateRoutes(prep *prepared, keys []PathKey, mtu map[string]int, guaranteed *int, possible *int) {
	chosen := make(map[PathKey]Route, len(keys))
	var dfs func(int)
	dfs = func(index int) {
		if index == len(keys) {
			if !receiptsSatisfied(prep, chosen, mtu) {
				return
			}
			payload := targetPayload(prep, chosen, mtu)
			if payload < *guaranteed {
				*guaranteed = payload
			}
			if payload > *possible {
				*possible = payload
			}
			return
		}
		key := keys[index]
		for _, route := range prep.routeSets[key] {
			chosen[key] = route
			dfs(index + 1)
		}
	}
	dfs(0)
}

func receiptsSatisfied(prep *prepared, chosen map[PathKey]Route, mtu map[string]int) bool {
	for _, receipt := range prep.receipts {
		route := chosen[PathKey{Generation: receipt.Generation, Label: receipt.Label}]
		wires, ok := routeWires(receipt.SendLength, route)
		if !ok {
			return false
		}
		failed := false
		for index, item := range route.Segments {
			if wires[index] > mtu[item.LinkID] {
				failed = true
				break
			}
		}
		if failed != receipt.Failed {
			return false
		}
	}
	return true
}

func targetPayload(prep *prepared, chosen map[PathKey]Route, mtu map[string]int) int {
	if prep.in.Target.RouteID != "" {
		for _, route := range prep.routeSets[prep.targetKey] {
			if route.ID == prep.in.Target.RouteID {
				payload, ok := maxPayloadForRoute(route, mtu, prep.in.Context)
				if !ok {
					return -1
				}
				return payload
			}
		}
		return -1
	}
	route := chosen[prep.targetKey]
	payload, ok := maxPayloadForRoute(route, mtu, prep.in.Context)
	if !ok {
		return -1
	}
	return payload
}

func sortKeys(keys []PathKey) {
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[j].Generation < keys[i].Generation || (keys[j].Generation == keys[i].Generation && keys[j].Label < keys[i].Label) {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
}

func sortStrings(values []string) {
	for i := 0; i < len(values); i++ {
		for j := i + 1; j < len(values); j++ {
			if values[j] < values[i] {
				values[i], values[j] = values[j], values[i]
			}
		}
	}
}
