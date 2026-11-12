package taskpkg

import (
	"errors"
	"fmt"
	"math"
)

// RGBA is one RGBA sample. Color channels use straight alpha unless a node
// explicitly converts them to premultiplied alpha.
type RGBA [4]float64

// ColorMatrix is applied only to RGB channels; alpha is passed through.
type ColorMatrix [3][3]float64

// Node is a node in the differentiable color pipeline.
type Node interface {
	nodeMarker()
}

// InputNode selects one RGBA input from the pipeline input vector.
type InputNode struct {
	Index int
}

// MatrixNode applies a 3x3 matrix to the RGB channels.
type MatrixNode struct {
	Input  Node
	Matrix ColorMatrix
}

// PowerSegment describes one power-law interval.
type PowerSegment struct {
	InputMin  float64
	InputMax  float64
	OutputMin float64
	OutputMax float64
	Gamma     float64
}

// PowerNode applies the same piecewise power transfer function to RGB.
type PowerNode struct {
	Input    Node
	Segments []PowerSegment
}

// PremultiplyNode converts straight RGBA into premultiplied RGBA.
type PremultiplyNode struct {
	Input Node
}

// UnpremultiplyNode converts premultiplied RGBA into straight RGBA.
type UnpremultiplyNode struct {
	Input Node
}

// OverNode composites Source over Destination using premultiplied RGBA.
type OverNode struct {
	Source      Node
	Destination Node
}

// NewInputNode creates an RGBA input node.
func NewInputNode(index int) *InputNode {
	return &InputNode{Index: index}
}

// NewMatrixNode creates an RGB matrix node.
func NewMatrixNode(input Node, matrix ColorMatrix) *MatrixNode {
	return &MatrixNode{Input: input, Matrix: matrix}
}

// NewPowerNode creates a piecewise power transfer node. The segment slice is
// copied so later caller mutations do not change the node.
func NewPowerNode(input Node, segments []PowerSegment) *PowerNode {
	copied := append([]PowerSegment(nil), segments...)
	return &PowerNode{Input: input, Segments: copied}
}

// NewPremultiplyNode creates an alpha premultiplication node.
func NewPremultiplyNode(input Node) *PremultiplyNode {
	return &PremultiplyNode{Input: input}
}

// NewUnpremultiplyNode creates an alpha unpremultiplication node.
func NewUnpremultiplyNode(input Node) *UnpremultiplyNode {
	return &UnpremultiplyNode{Input: input}
}

// NewOverNode creates a premultiplied Porter-Duff over node.
func NewOverNode(source, destination Node) *OverNode {
	return &OverNode{Source: source, Destination: destination}
}

func (*InputNode) nodeMarker()         {}
func (*MatrixNode) nodeMarker()        {}
func (*PowerNode) nodeMarker()         {}
func (*PremultiplyNode) nodeMarker()   {}
func (*UnpremultiplyNode) nodeMarker() {}
func (*OverNode) nodeMarker()          {}

type evaluatedNode struct {
	value    RGBA
	jacobian [][]float64
}

// Evaluate runs the graph and returns output RGBA and the 4 x len(inputs)
// Jacobian of the output with respect to every input component.
func Evaluate(root Node, inputs []float64) (RGBA, [][]float64, error) {
	if root == nil {
		return RGBA{}, nil, errors.New("pipeline root is nil")
	}
	if len(inputs) == 0 || len(inputs)%4 != 0 {
		return RGBA{}, nil, fmt.Errorf("input length must be a positive multiple of 4, got %d", len(inputs))
	}
	for i, value := range inputs {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return RGBA{}, nil, fmt.Errorf("input component %d is not finite", i)
		}
	}

	memo := make(map[Node]evaluatedNode)
	active := make(map[Node]bool)
	result, err := evaluateNode(root, inputs, memo, active)
	if err != nil {
		return RGBA{}, nil, err
	}
	return result.value, result.jacobian, nil
}

func evaluateNode(node Node, inputs []float64, memo map[Node]evaluatedNode, active map[Node]bool) (evaluatedNode, error) {
	if node == nil {
		return evaluatedNode{}, errors.New("pipeline contains a nil node")
	}
	if result, ok := memo[node]; ok {
		return result, nil
	}
	if active[node] {
		return evaluatedNode{}, errors.New("pipeline contains a cycle")
	}

	active[node] = true

	var result evaluatedNode
	var err error
	switch n := node.(type) {
	case *InputNode:
		result, err = evaluateInput(n, inputs)
	case *MatrixNode:
		result, err = evaluateUnary(n.Input, inputs, memo, active, matrixTransform(n))
	case *PowerNode:
		if err = validatePowerSegments(n.Segments); err != nil {
			return evaluatedNode{}, err
		}
		result, err = evaluateUnary(n.Input, inputs, memo, active, powerTransform(n.Segments))
	case *PremultiplyNode:
		result, err = evaluateUnary(n.Input, inputs, memo, active, premultiplyTransform)
	case *UnpremultiplyNode:
		result, err = evaluateUnary(n.Input, inputs, memo, active, unpremultiplyTransform)
	case *OverNode:
		result, err = evaluateOver(n, inputs, memo, active)
	default:
		err = fmt.Errorf("unsupported node type %T", node)
	}
	if err != nil {
		delete(active, node)
		return evaluatedNode{}, err
	}

	memo[node] = result
	delete(active, node)
	return result, nil
}

func evaluateInput(node *InputNode, inputs []float64) (evaluatedNode, error) {
	inputCount := len(inputs) / 4
	if node.Index < 0 || node.Index >= inputCount {
		return evaluatedNode{}, fmt.Errorf("input index %d is outside [0, %d)", node.Index, inputCount)
	}

	value := RGBA{}
	jacobian := zeroJacobian(4, len(inputs))
	for row := 0; row < 4; row++ {
		column := node.Index*4 + row
		value[row] = inputs[column]
		jacobian[row][column] = 1
	}
	return evaluatedNode{value: value, jacobian: jacobian}, nil
}

type localTransform func(RGBA) (RGBA, [4][4]float64, error)

func evaluateUnary(
	child Node,
	inputs []float64,
	memo map[Node]evaluatedNode,
	active map[Node]bool,
	transform localTransform,
) (evaluatedNode, error) {
	childResult, err := evaluateNode(child, inputs, memo, active)
	if err != nil {
		return evaluatedNode{}, err
	}
	value, local, err := transform(childResult.value)
	if err != nil {
		return evaluatedNode{}, err
	}
	return evaluatedNode{
		value:    value,
		jacobian: chainJacobian(local, childResult.jacobian),
	}, nil
}

func evaluateOver(
	node *OverNode,
	inputs []float64,
	memo map[Node]evaluatedNode,
	active map[Node]bool,
) (evaluatedNode, error) {
	source, err := evaluateNode(node.Source, inputs, memo, active)
	if err != nil {
		return evaluatedNode{}, err
	}
	destination, err := evaluateNode(node.Destination, inputs, memo, active)
	if err != nil {
		return evaluatedNode{}, err
	}

	sourceAlpha := source.value[3]
	remainingAlpha := 1 - sourceAlpha
	value := RGBA{}
	sourceLocal := identityLocalMatrix()
	destinationLocal := zeroLocalMatrix()

	for channel := 0; channel < 3; channel++ {
		value[channel] = source.value[channel] + remainingAlpha*destination.value[channel]
		sourceLocal[channel][3] = -destination.value[channel]
		destinationLocal[channel][channel] = remainingAlpha
	}
	value[3] = sourceAlpha + remainingAlpha*destination.value[3]
	sourceLocal[3][3] = 1 - destination.value[3]
	destinationLocal[3][3] = remainingAlpha

	jacobian := addJacobian(
		chainJacobian(sourceLocal, source.jacobian),
		chainJacobian(destinationLocal, destination.jacobian),
	)
	return evaluatedNode{value: value, jacobian: jacobian}, nil
}

func matrixTransform(node *MatrixNode) localTransform {
	return func(value RGBA) (RGBA, [4][4]float64, error) {
		for row := 0; row < 3; row++ {
			for column := 0; column < 3; column++ {
				entry := node.Matrix[row][column]
				if math.IsNaN(entry) || math.IsInf(entry, 0) {
					return RGBA{}, [4][4]float64{}, fmt.Errorf("matrix entry [%d][%d] is not finite", row, column)
				}
			}
		}

		result := RGBA{0, 0, 0, value[3]}
		local := identityLocalMatrix()
		for row := 0; row < 3; row++ {
			sum := 0.0
			for column := 0; column < 3; column++ {
				entry := node.Matrix[row][column]
				sum += entry * value[column]
				local[row][column] = entry
			}
			result[row] = sum
		}
		return result, local, nil
	}
}

func powerTransform(segments []PowerSegment) localTransform {
	return func(value RGBA) (RGBA, [4][4]float64, error) {
		result := RGBA{0, 0, 0, value[3]}
		local := identityLocalMatrix()
		for channel := 0; channel < 3; channel++ {
			mapped, derivative := applyPowerSegment(value[channel], segments)
			result[channel] = mapped
			local[channel][channel] = derivative
		}
		return result, local, nil
	}
}

func premultiplyTransform(value RGBA) (RGBA, [4][4]float64, error) {
	alpha := value[3]
	result := RGBA{0, 0, 0, alpha}
	local := zeroLocalMatrix()
	for channel := 0; channel < 3; channel++ {
		result[channel] = value[channel] * alpha
		local[channel][channel] = alpha
		local[channel][3] = value[channel]
	}
	local[3][3] = 1
	return result, local, nil
}

func unpremultiplyTransform(value RGBA) (RGBA, [4][4]float64, error) {
	result := RGBA{}
	local := zeroLocalMatrix()
	local[3][3] = 1

	if value[3] <= 0 {
		return result, local, nil
	}

	alpha := value[3]
	inverseAlpha := 1 / alpha
	result[3] = alpha
	for channel := 0; channel < 3; channel++ {
		result[channel] = value[channel] * inverseAlpha
		local[channel][channel] = inverseAlpha
		local[channel][3] = -value[channel] * inverseAlpha * inverseAlpha
	}
	return result, local, nil
}

func validatePowerSegments(segments []PowerSegment) error {
	if len(segments) == 0 {
		return errors.New("power node requires at least one segment")
	}
	for i, segment := range segments {
		if !finite(segment.InputMin) || !finite(segment.InputMax) ||
			!finite(segment.OutputMin) || !finite(segment.OutputMax) || !finite(segment.Gamma) {
			return fmt.Errorf("power segment %d contains a non-finite value", i)
		}
		if segment.InputMin >= segment.InputMax {
			return fmt.Errorf("power segment %d must have InputMin < InputMax", i)
		}
		if segment.Gamma <= 0 {
			return fmt.Errorf("power segment %d must have Gamma > 0", i)
		}
		if i > 0 && segment.InputMin != segments[i-1].InputMax {
			return fmt.Errorf("power segments %d and %d are not contiguous", i-1, i)
		}
	}
	return nil
}

func applyPowerSegment(x float64, segments []PowerSegment) (float64, float64) {
	first := segments[0]
	last := segments[len(segments)-1]
	if x <= first.InputMin {
		if x == first.InputMin {
			return powerOnSegment(x, first)
		}
		return first.OutputMin, 0
	}
	if x >= last.InputMax {
		return last.OutputMax, 0
	}

	for _, segment := range segments {
		if x >= segment.InputMin && x < segment.InputMax {
			return powerOnSegment(x, segment)
		}
	}
	return last.OutputMax, 0
}

func powerOnSegment(x float64, segment PowerSegment) (float64, float64) {
	inputWidth := segment.InputMax - segment.InputMin
	outputWidth := segment.OutputMax - segment.OutputMin
	t := (x - segment.InputMin) / inputWidth
	value := segment.OutputMin + outputWidth*math.Pow(t, segment.Gamma)

	if outputWidth == 0 {
		return value, 0
	}
	slope := outputWidth / inputWidth
	if t == 0 {
		switch {
		case segment.Gamma == 1:
			return value, slope
		case segment.Gamma > 1:
			return value, 0
		default:
			return value, slope * math.Inf(1)
		}
	}
	derivative := slope * segment.Gamma * math.Pow(t, segment.Gamma-1)
	return value, derivative
}

func chainJacobian(local [4][4]float64, child [][]float64) [][]float64 {
	columns := len(child[0])
	result := zeroJacobian(4, columns)
	for row := 0; row < 4; row++ {
		for column := 0; column < columns; column++ {
			sum := 0.0
			for k := 0; k < 4; k++ {
				sum += local[row][k] * child[k][column]
			}
			result[row][column] = sum
		}
	}
	return result
}

func addJacobian(left, right [][]float64) [][]float64 {
	result := zeroJacobian(len(left), len(left[0]))
	for row := range result {
		for column := range result[row] {
			result[row][column] = left[row][column] + right[row][column]
		}
	}
	return result
}

func zeroJacobian(rows, columns int) [][]float64 {
	values := make([]float64, rows*columns)
	result := make([][]float64, rows)
	for row := range result {
		result[row] = values[row*columns : (row+1)*columns]
	}
	return result
}

func identityLocalMatrix() [4][4]float64 {
	local := zeroLocalMatrix()
	for i := 0; i < 4; i++ {
		local[i][i] = 1
	}
	return local
}

func zeroLocalMatrix() [4][4]float64 {
	return [4][4]float64{}
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
