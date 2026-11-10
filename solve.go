package main

import (
	"errors"
	"strings"
)

type GatewayRoute struct {
	Prefix      string `json:"prefix"`
	Replacement string `json:"replacement"`
	Role        string `json:"role"`
}

type OriginRoute struct {
	Prefix      string `json:"prefix"`
	Replacement string `json:"replacement"`
	Backend     string `json:"backend"`
}

type Trace struct {
	Path           string
	Length         int
	GatewayRoute   int
	OriginRoute    int
	GatewayRewrite string
	OriginRewrite  string
	Backend        string
}

func FindShortestPath(
	alphabet string,
	maxPathLength int,
	gatewayRoutes []GatewayRoute,
	originRoutes []OriginRoute,
	role string,
	restrictedBackends map[string]bool,
) (string, *Trace, error) {
	if alphabet == "" {
		return "", nil, errors.New("alphabet must not be empty")
	}
	if maxPathLength < 0 {
		return "", nil, errors.New("max path length must not be negative")
	}

	rank := make(map[byte]int, len(alphabet))
	for index := 0; index < len(alphabet); index++ {
		symbol := alphabet[index]
		if _, exists := rank[symbol]; exists {
			return "", nil, errors.New("alphabet must not contain duplicate bytes")
		}
		rank[symbol] = index
	}

	gatewayPrefixes := gatewayRoutePrefixes(gatewayRoutes)
	originPrefixes := originRoutePrefixes(originRoutes)
	var bestPath string
	var bestTrace *Trace

	for gatewayIndex, gatewayRoute := range gatewayRoutes {
		if gatewayRoute.Role != role {
			continue
		}

		for originIndex, originRoute := range originRoutes {
			if !restrictedBackends[originRoute.Backend] {
				continue
			}

			suffix, possible := requiredSuffix(gatewayRoute.Replacement, originRoute.Prefix)
			if !possible {
				continue
			}

			path := gatewayRoute.Prefix + suffix
			if len(path) > maxPathLength || !isAlphabetString(path, rank) {
				continue
			}
			if hasEarlierPrefix(gatewayPrefixes, gatewayIndex, path) {
				continue
			}

			gatewayRewrite := gatewayRoute.Replacement + suffix
			if !strings.HasPrefix(gatewayRewrite, originRoute.Prefix) {
				continue
			}
			if hasEarlierPrefix(originPrefixes, originIndex, gatewayRewrite) {
				continue
			}

			originRewrite := originRoute.Replacement + gatewayRewrite[len(originRoute.Prefix):]
			trace := &Trace{
				Path:           path,
				Length:         len(path),
				GatewayRoute:   gatewayIndex,
				OriginRoute:    originIndex,
				GatewayRewrite: gatewayRewrite,
				OriginRewrite:  originRewrite,
				Backend:        originRoute.Backend,
			}

			if bestTrace == nil || lessInAlphabet(path, bestPath, rank) {
				bestPath = path
				bestTrace = trace
			}
		}
	}

	return bestPath, bestTrace, nil
}

func requiredSuffix(replacement string, prefix string) (string, bool) {
	if strings.HasPrefix(prefix, replacement) {
		return prefix[len(replacement):], true
	}
	if strings.HasPrefix(replacement, prefix) {
		return "", true
	}
	return "", false
}

func isAlphabetString(value string, rank map[byte]int) bool {
	for index := 0; index < len(value); index++ {
		if _, exists := rank[value[index]]; !exists {
			return false
		}
	}
	return true
}

func gatewayRoutePrefixes(routes []GatewayRoute) []string {
	prefixes := make([]string, len(routes))
	for index, route := range routes {
		prefixes[index] = route.Prefix
	}
	return prefixes
}

func originRoutePrefixes(routes []OriginRoute) []string {
	prefixes := make([]string, len(routes))
	for index, route := range routes {
		prefixes[index] = route.Prefix
	}
	return prefixes
}

func hasEarlierPrefix(prefixes []string, routeIndex int, value string) bool {
	for index := 0; index < routeIndex; index++ {
		if strings.HasPrefix(value, prefixes[index]) {
			return true
		}
	}
	return false
}

func lessInAlphabet(left string, right string, rank map[byte]int) bool {
	if len(left) != len(right) {
		return len(left) < len(right)
	}

	limit := len(left)
	if len(right) < limit {
		limit = len(right)
	}

	for index := 0; index < limit; index++ {
		leftRank := rank[left[index]]
		rightRank := rank[right[index]]
		if leftRank != rightRank {
			return leftRank < rightRank
		}
	}

	return len(left) < len(right)
}

func matchGatewayRoute(routes []GatewayRoute, path string) int {
	for index, route := range routes {
		if strings.HasPrefix(path, route.Prefix) {
			return index
		}
	}
	return -1
}

func matchOriginRoute(routes []OriginRoute, path string) int {
	for index, route := range routes {
		if strings.HasPrefix(path, route.Prefix) {
			return index
		}
	}
	return -1
}

func replacePrefix(path string, prefix string, replacement string) string {
	return replacement + path[len(prefix):]
}
