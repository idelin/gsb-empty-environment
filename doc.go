// Package iac compiles declarative infrastructure resources, remote drift and
// lifecycle rules into a validated, executable DAG.
//
// The planner applies these transformations in order:
//
//  1. Select the apply target's transitive upstream closure, or the destroy
//     target's transitive downstream closure.
//  2. Resolve proposed, drift-detected and ForceNew changes.
//  3. Cascade replace_triggered_by replacement upgrades.
//  4. Propagate create_before_destroy upstream to replacement dependencies.
//  5. Reject forbid_cbd conflicts and any plan that destroys a
//     prevent_destroy resource.
//  6. Split replacement resources into create/destroy nodes, wire ordering,
//     reject cycles and emit maximal-concurrency execution waves.
//
// BuildPlan is the main entry point. Graphs can be executed directly with
// Graph.Execute or through Plan.Execute; failures only skip transitive
// dependents, leaving independent branches runnable.
package iac
