package netsec

import "sort"

// MaxPassDepth is the maximum number of PassRole hops in a chain, matching
// the "at most 3 role passes" requirement.
const MaxPassDepth = 3

// Reachability describes one role reachable from the entry principal.
type Reachability struct {
	// RoleARN is the reachable role.
	RoleARN string
	// Depth is the number of PassRole operations used, from 1 to MaxPassDepth.
	Depth int
	// Path lists role ARNs from the first passed role to this role.
	Path []string
}

type passNode struct {
	role     Role
	depth    int
	path     []string
	policies []Policy
}

// ReachableRoles performs a breadth-first search over PassRole + Lambda
// escalation. At each hop the current identity must:
//
//   - hold lambda:CreateFunction and lambda:InvokeFunction,
//   - be allowed to pass the next role to Lambda (Deny always wins), and
//   - have the next role trust the Lambda service principal.
//
// Roles are visited once at their shortest depth and chains stop at
// MaxPassDepth. The result is sorted by role ARN.
func ReachableRoles(entryPolicies []Policy, roles []Role) []Reachability {
	var queue []passNode
	queue = append(queue, passNode{policies: entryPolicies})

	seen := make(map[string]bool)
	var results []Reachability

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if current.depth >= MaxPassDepth {
			continue
		}
		if !CanCreateAndInvokeLambda(current.policies) {
			continue
		}

		for _, next := range roles {
			if seen[next.ARN] {
				continue
			}
			if !CanPassRole(current.policies, next.ARN, ServiceLambda) {
				continue
			}
			if !next.TrustsService(ServiceLambda) {
				continue
			}

			seen[next.ARN] = true
			nextDepth := current.depth + 1
			nextPath := append(append([]string{}, current.path...), next.ARN)
			results = append(results, Reachability{
				RoleARN: next.ARN,
				Depth:   nextDepth,
				Path:    nextPath,
			})

			if nextDepth < MaxPassDepth {
				queue = append(queue, passNode{
					role:     next,
					depth:    nextDepth,
					path:     append([]string{}, nextPath...),
					policies: next.Policies,
				})
			}
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].RoleARN < results[j].RoleARN
	})
	return results
}
