package netsec

import (
	"math/rand"
	"testing"
)

func cubeMatchesByte(c bitCube, value byte) bool {
	return value&c.mask[0] == c.value[0]
}

func TestCubeCoverageMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for iteration := 0; iteration < 5000; iteration++ {
		mask := byte(rng.Intn(256))
		value := byte(rng.Intn(256)) & mask
		c := mustTestCube(value, mask)

		count := 1 + rng.Intn(3)
		allowed := make([]bitCube, 0, count)
		for i := 0; i < count; i++ {
			am := byte(rng.Intn(256))
			av := byte(rng.Intn(256)) & am
			allowed = append(allowed, mustTestCube(av, am))
		}

		got := cubeCovered(c, allowed)
		want := true
		for v := 0; v < 256; v++ {
			if !cubeMatchesByte(c, byte(v)) {
				continue
			}
			inUnion := false
			for _, a := range allowed {
				if cubeMatchesByte(a, byte(v)) {
					inUnion = true
					break
				}
			}
			if !inUnion {
				want = false
				break
			}
		}
		if got != want {
			t.Fatalf(
				"iteration %d: cubeCovered=%v, brute force=%v, c=%x/%x allowed=%v",
				iteration, got, want, c.value[0], c.mask[0], allowed,
			)
		}
	}
}

func TestCubeSubsetMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for iteration := 0; iteration < 5000; iteration++ {
		cm := byte(rng.Intn(256))
		cv := byte(rng.Intn(256)) & cm
		am := byte(rng.Intn(256))
		av := byte(rng.Intn(256)) & am
		c := mustTestCube(cv, cm)
		a := mustTestCube(av, am)

		got := cubeSubset(c, a)
		want := true
		for v := 0; v < 256; v++ {
			if cubeMatchesByte(c, byte(v)) && !cubeMatchesByte(a, byte(v)) {
				want = false
				break
			}
		}
		if got != want {
			t.Fatalf(
				"iteration %d: cubeSubset=%v, brute force=%v, c=%x/%x a=%x/%x",
				iteration, got, want, cv, cm, av, am,
			)
		}
	}
}

func mustTestCube(value, mask byte) bitCube {
	return bitCube{value: []byte{value}, mask: []byte{mask}}
}

func TestActionEqualityUsesParameterKeys(t *testing.T) {
	left := Action{Kind: "set", Params: map[string]string{"x": ""}}
	right := Action{Kind: "set", Params: map[string]string{"y": ""}}
	if left.Equal(right) {
		t.Fatal("actions with different parameter keys compared equal")
	}

	same := Action{Kind: "forward", Ports: []uint32{2, 1, 2}, Params: map[string]string{"x": "v"}}
	other := Action{Kind: "forward", Ports: []uint32{1, 2}, Params: map[string]string{"x": "v"}}
	if !same.Equal(other) {
		t.Fatal("actions differing only by port order and duplicates compared unequal")
	}
}
