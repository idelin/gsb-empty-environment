package main

import (
	"math/rand"
	"testing"
)

func TestFindShortestPathBasicRewrite(t *testing.T) {
	gateway := []GatewayRoute{
		{Prefix: "b", Replacement: "public", Role: "user"},
		{Prefix: "a", Replacement: "admin", Role: "user"},
	}
	origin := []OriginRoute{
		{Prefix: "public", Replacement: "/p", Backend: "public"},
		{Prefix: "admin", Replacement: "/a", Backend: "admin"},
	}

	path, trace, err := FindShortestPath("ab", 16, gateway, origin, "user", map[string]bool{"admin": true})
	if err != nil {
		t.Fatal(err)
	}
	if path != "a" || trace.Backend != "admin" || trace.GatewayRewrite != "admin" || trace.OriginRewrite != "/a" {
		t.Fatalf("unexpected result: path=%q trace=%+v", path, trace)
	}
}

func TestGatewayShadowBlocksUnauthorizedRoute(t *testing.T) {
	gateway := []GatewayRoute{
		{Prefix: "a", Replacement: "deny", Role: "admin"},
		{Prefix: "ab", Replacement: "admin", Role: "user"},
	}
	origin := []OriginRoute{{Prefix: "admin", Backend: "admin"}}

	path, trace, err := FindShortestPath("ab", 16, gateway, origin, "user", map[string]bool{"admin": true})
	if err != nil {
		t.Fatal(err)
	}
	if trace != nil {
		t.Fatalf("expected no path, got %q via %+v", path, trace)
	}
}

func TestOriginShadowBlocksRestrictedRoute(t *testing.T) {
	gateway := []GatewayRoute{{Prefix: "a", Replacement: "admin", Role: "user"}}
	origin := []OriginRoute{
		{Prefix: "admin", Backend: "public"},
		{Prefix: "admin/secret", Backend: "admin"},
	}

	_, trace, err := FindShortestPath("ab", 16, gateway, origin, "user", map[string]bool{"admin": true})
	if err != nil {
		t.Fatal(err)
	}
	if trace != nil {
		t.Fatalf("expected restricted origin route to be shadowed, got %+v", trace)
	}
}

func TestOverlapRequiresSuffixAndAlphabetOrder(t *testing.T) {
	gateway := []GatewayRoute{
		{Prefix: "g2", Replacement: "adm", Role: "user"},
		{Prefix: "g1", Replacement: "adm", Role: "user"},
	}
	origin := []OriginRoute{{Prefix: "admin", Replacement: "", Backend: "admin"}}

	path, trace, err := FindShortestPath("g12inadm", 16, gateway, origin, "user", map[string]bool{"admin": true})
	if err != nil {
		t.Fatal(err)
	}
	if path != "g1in" {
		t.Fatalf("expected lexicographically shortest g1in, got %q", path)
	}
	if trace.GatewayRewrite != "admin" || trace.OriginRoute != 0 {
		t.Fatalf("unexpected trace: %+v", trace)
	}
}

func TestMaxPathLength(t *testing.T) {
	gateway := []GatewayRoute{{Prefix: "g", Replacement: "adm", Role: "user"}}
	origin := []OriginRoute{{Prefix: "admin", Backend: "admin"}}

	_, trace, err := FindShortestPath("aginmd", 2, gateway, origin, "user", map[string]bool{"admin": true})
	if err != nil {
		t.Fatal(err)
	}
	if trace != nil {
		t.Fatalf("path of length 3 should exceed max length 2")
	}

	path, trace, err := FindShortestPath("aginmd", 3, gateway, origin, "user", map[string]bool{"admin": true})
	if err != nil {
		t.Fatal(err)
	}
	if path != "gin" || len(path) != trace.Length {
		t.Fatalf("unexpected path %q trace %+v", path, trace)
	}
}

func TestLargeMaxDoesNotEnumerate(t *testing.T) {
	gateway := []GatewayRoute{{Prefix: "x", Replacement: "admin", Role: "user"}}
	origin := []OriginRoute{{Prefix: "admin", Backend: "admin"}}

	path, trace, err := FindShortestPath("x", 1<<30, gateway, origin, "user", map[string]bool{"admin": true})
	if err != nil {
		t.Fatal(err)
	}
	if path != "x" || trace.Length != 1 {
		t.Fatalf("unexpected result: %q %+v", path, trace)
	}
}

func TestEmptyPrefixAndEmptyPath(t *testing.T) {
	gateway := []GatewayRoute{{Prefix: "", Replacement: "admin", Role: "user"}}
	origin := []OriginRoute{{Prefix: "admin", Replacement: "/", Backend: "admin"}}

	path, trace, err := FindShortestPath("ab", 0, gateway, origin, "user", map[string]bool{"admin": true})
	if err != nil {
		t.Fatal(err)
	}
	if path != "" || trace.Length != 0 || trace.GatewayRewrite != "admin" {
		t.Fatalf("unexpected empty-path result: path=%q trace=%+v", path, trace)
	}
}

func TestIncompatiblePrefixesDoNotMatch(t *testing.T) {
	gateway := []GatewayRoute{{Prefix: "a", Replacement: "admin", Role: "user"}}
	origin := []OriginRoute{{Prefix: "billing", Backend: "admin"}}

	_, trace, err := FindShortestPath("ab", 16, gateway, origin, "user", map[string]bool{"admin": true})
	if err != nil {
		t.Fatal(err)
	}
	if trace != nil {
		t.Fatalf("admin and billing are not prefix-compatible, got %+v", trace)
	}
}

func TestRandomSmallAlphabetMatchesBruteForce(t *testing.T) {
	random := rand.New(rand.NewSource(42))
	symbols := "ab"

	for iteration := 0; iteration < 200; iteration++ {
		gateway := randomGatewayRoutes(random, symbols)
		origin := randomOriginRoutes(random)
		restricted := map[string]bool{"admin": true}
		maxLength := 6

		got, gotTrace, err := FindShortestPath(symbols, maxLength, gateway, origin, "user", restricted)
		if err != nil {
			t.Fatal(err)
		}
		want, wantTrace := bruteForceShortestPath(symbols, maxLength, gateway, origin, "user", restricted)
		if got != want {
			t.Fatalf("iteration %d: got %q, want %q\ngateway=%#v\norigin=%#v", iteration, got, want, gateway, origin)
		}
		if !sameTrace(gotTrace, wantTrace) {
			t.Fatalf("iteration %d: trace mismatch got=%+v want=%+v", iteration, gotTrace, wantTrace)
		}
	}
}

func sameTrace(got *Trace, want *Trace) bool {
	if got == nil || want == nil {
		return got == want
	}
	return got.Path == want.Path &&
		got.Length == want.Length &&
		got.GatewayRoute == want.GatewayRoute &&
		got.OriginRoute == want.OriginRoute &&
		got.GatewayRewrite == want.GatewayRewrite &&
		got.OriginRewrite == want.OriginRewrite &&
		got.Backend == want.Backend
}

func randomGatewayRoutes(random *rand.Rand, symbols string) []GatewayRoute {
	count := 1 + random.Intn(3)
	routes := make([]GatewayRoute, count)
	for index := range routes {
		routes[index] = GatewayRoute{
			Prefix:      randomWord(random, symbols, 3),
			Replacement: chooseReplacement(random),
			Role:        []string{"user", "admin"}[random.Intn(2)],
		}
	}
	return routes
}

func randomOriginRoutes(random *rand.Rand) []OriginRoute {
	count := 1 + random.Intn(3)
	routes := make([]OriginRoute, count)
	backends := []string{"public", "admin", "billing"}
	for index := range routes {
		routes[index] = OriginRoute{
			Prefix:      choosePrefix(random),
			Replacement: "",
			Backend:     backends[random.Intn(len(backends))],
		}
	}
	return routes
}

func randomWord(random *rand.Rand, symbols string, maxLength int) string {
	length := random.Intn(maxLength + 1)
	bytes := make([]byte, length)
	for index := range bytes {
		bytes[index] = symbols[random.Intn(len(symbols))]
	}
	return string(bytes)
}

func chooseReplacement(random *rand.Rand) string {
	return []string{"admin", "adm", "public", "billing", "bill", ""}[random.Intn(6)]
}

func choosePrefix(random *rand.Rand) string {
	return []string{"admin", "adm", "public", "billing", "bill", ""}[random.Intn(6)]
}

func bruteForceShortestPath(
	alphabet string,
	maxLength int,
	gateway []GatewayRoute,
	origin []OriginRoute,
	role string,
	restricted map[string]bool,
) (string, *Trace) {
	type queueItem struct {
		path string
	}

	queue := []queueItem{{path: ""}}
	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]

		if trace := evaluatePath(item.path, gateway, origin, role, restricted); trace != nil {
			return item.path, trace
		}
		if len(item.path) == maxLength {
			continue
		}
		for index := 0; index < len(alphabet); index++ {
			queue = append(queue, queueItem{path: item.path + string(alphabet[index])})
		}
	}

	return "", nil
}

func evaluatePath(
	path string,
	gateway []GatewayRoute,
	origin []OriginRoute,
	role string,
	restricted map[string]bool,
) *Trace {
	gatewayIndex := matchGatewayRoute(gateway, path)
	if gatewayIndex < 0 || gateway[gatewayIndex].Role != role {
		return nil
	}

	gatewayRoute := gateway[gatewayIndex]
	gatewayRewrite := replacePrefix(path, gatewayRoute.Prefix, gatewayRoute.Replacement)
	originIndex := matchOriginRoute(origin, gatewayRewrite)
	if originIndex < 0 || !restricted[origin[originIndex].Backend] {
		return nil
	}

	originRoute := origin[originIndex]
	originRewrite := replacePrefix(gatewayRewrite, originRoute.Prefix, originRoute.Replacement)
	return &Trace{
		Path:           path,
		Length:         len(path),
		GatewayRoute:   gatewayIndex,
		OriginRoute:    originIndex,
		GatewayRewrite: gatewayRewrite,
		OriginRewrite:  originRewrite,
		Backend:        originRoute.Backend,
	}
}
