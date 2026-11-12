package taskpkg

import (
	"math"
	"testing"
)

func TestEvaluateFullPipelineFiniteDifference(t *testing.T) {
	matrixA := ColorMatrix{
		{1.05, -0.05, 0.02},
		{0.03, 0.96, 0.01},
		{-0.02, 0.04, 0.98},
	}
	matrixB := ColorMatrix{
		{0.90, 0.04, 0.02},
		{0.01, 1.02, -0.03},
		{0.03, 0.00, 0.97},
	}
	segments := []PowerSegment{
		{InputMin: 0, InputMax: 0.45, OutputMin: 0, OutputMax: 0.25, Gamma: 0.8},
		{InputMin: 0.45, InputMax: 1, OutputMin: 0.25, OutputMax: 1, Gamma: 2},
	}

	source := NewPremultiplyNode(
		NewMatrixNode(
			NewPowerNode(NewInputNode(0), segments),
			matrixA,
		),
	)
	destination := NewPremultiplyNode(
		NewMatrixNode(
			NewPowerNode(NewInputNode(1), segments),
			matrixB,
		),
	)
	base := NewPremultiplyNode(
		NewMatrixNode(
			NewPowerNode(NewInputNode(2), segments),
			matrixA,
		),
	)
	root := NewOverNode(source, NewOverNode(destination, base))

	inputs := []float64{
		0.18, 0.31, 0.27, 0.72,
		0.62, 0.38, 0.71, 0.46,
		0.41, 0.57, 0.22, 0.88,
	}

	assertFiniteDifference(t, root, inputs, 1e-6, 2e-5)
}

func TestMatrixAndPowerFiniteDifferenceAwayFromKnots(t *testing.T) {
	matrix := ColorMatrix{
		{0.8, 0.1, 0.2},
		{0.05, 1.1, -0.15},
		{0.25, 0.0, 0.75},
	}
	segments := []PowerSegment{
		{InputMin: 0, InputMax: 0.5, OutputMin: 0, OutputMax: 0.3, Gamma: 0.7},
		{InputMin: 0.5, InputMax: 1, OutputMin: 0.3, OutputMax: 1, Gamma: 2.5},
	}
	root := NewMatrixNode(
		NewPowerNode(NewInputNode(0), segments),
		matrix,
	)
	inputs := []float64{0.12, 0.73, 0.34, 0.64}

	assertFiniteDifference(t, root, inputs, 1e-6, 1e-5)
}

func TestTransparentBoundaryAnalyticUnpremultiply(t *testing.T) {
	root := NewUnpremultiplyNode(NewInputNode(0))
	inputs := []float64{0.7, -0.2, 0.4, 0}

	value, jacobian, err := Evaluate(root, inputs)
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	wantValue := RGBA{0, 0, 0, 0}
	if value != wantValue {
		t.Fatalf("value = %v, want %v", value, wantValue)
	}

	wantJacobian := [][]float64{
		{0, 0, 0, 0},
		{0, 0, 0, 0},
		{0, 0, 0, 0},
		{0, 0, 0, 1},
	}
	assertMatrixClose(t, jacobian, wantJacobian, 0)
}

func TestPremultiplyAndUnpremultiplyFiniteDifference(t *testing.T) {
	root := NewUnpremultiplyNode(NewPremultiplyNode(NewInputNode(0)))
	inputs := []float64{0.27, 0.54, 0.81, 0.37}

	value, _, err := Evaluate(root, inputs)
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if !rgbaClose(value, RGBA{inputs[0], inputs[1], inputs[2], inputs[3]}, 1e-12) {
		t.Fatalf("round-trip value = %v, want input RGBA", value)
	}
	assertFiniteDifference(t, NewPremultiplyNode(NewInputNode(0)), inputs, 1e-6, 1e-7)
	assertFiniteDifference(t, root, inputs, 1e-6, 1e-7)
}

func TestTransparentOverBoundaryAnalytic(t *testing.T) {
	root := NewOverNode(NewInputNode(0), NewInputNode(1))
	inputs := []float64{
		0.3, 0.4, 0.5, 0,
		0.12, 0.24, 0.36, 0.75,
	}

	value, jacobian, err := Evaluate(root, inputs)
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	wantValue := RGBA{0.42, 0.64, 0.86, 0.75}
	if value != wantValue {
		t.Fatalf("value = %v, want %v", value, wantValue)
	}

	wantJacobian := [][]float64{
		{1, 0, 0, -0.12, 1, 0, 0, 0},
		{0, 1, 0, -0.24, 0, 1, 0, 0},
		{0, 0, 1, -0.36, 0, 0, 1, 0},
		{0, 0, 0, 0.25, 0, 0, 0, 1},
	}
	assertMatrixClose(t, jacobian, wantJacobian, 1e-12)
}

func TestPiecewisePowerUsesRightDerivativeAtKnot(t *testing.T) {
	segments := []PowerSegment{
		{InputMin: 0, InputMax: 0.5, OutputMin: 0, OutputMax: 0.25, Gamma: 2},
		{InputMin: 0.5, InputMax: 1, OutputMin: 0.25, OutputMax: 1, Gamma: 1},
	}
	root := NewPowerNode(NewInputNode(0), segments)

	value, jacobian, err := Evaluate(root, []float64{0.5, 0.5, 0.5, 0.5})
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if value[0] != 0.25 {
		t.Fatalf("mapped value = %v, want 0.25", value[0])
	}
	if got, want := jacobian[0][0], 1.5; got != want {
		t.Fatalf("right derivative = %v, want %v", got, want)
	}

	concave := []PowerSegment{
		{InputMin: 0, InputMax: 0.5, OutputMin: 0, OutputMax: 0.25, Gamma: 0.5},
		{InputMin: 0.5, InputMax: 1, OutputMin: 0.25, OutputMax: 1, Gamma: 1},
	}
	_, concaveJacobian, err := Evaluate(NewPowerNode(NewInputNode(0), concave), []float64{0, 0, 0, 0})
	if err != nil {
		t.Fatalf("concave Evaluate returned error: %v", err)
	}
	if !math.IsInf(concaveJacobian[0][0], 1) {
		t.Fatalf("concave right derivative = %v, want +Inf", concaveJacobian[0][0])
	}
}

func TestOverDoesNotRemultiplyPremultipliedColor(t *testing.T) {
	straightSource := NewInputNode(0)
	straightDestination := NewInputNode(1)
	root := NewOverNode(
		NewPremultiplyNode(straightSource),
		NewPremultiplyNode(straightDestination),
	)
	inputs := []float64{
		0.4, 0.6, 0.8, 0.5,
		0.2, 0.3, 0.4, 0.8,
	}

	value, _, err := Evaluate(root, inputs)
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}

	sourceAlpha := inputs[3]
	remainingAlpha := 1 - sourceAlpha
	want := RGBA{
		inputs[0] * sourceAlpha,
		inputs[1] * sourceAlpha,
		inputs[2] * sourceAlpha,
		sourceAlpha + remainingAlpha*inputs[7],
	}
	want[0] += remainingAlpha * inputs[4] * inputs[7]
	want[1] += remainingAlpha * inputs[5] * inputs[7]
	want[2] += remainingAlpha * inputs[6] * inputs[7]
	if !rgbaClose(value, want, 1e-12) {
		t.Fatalf("over value = %v, want %v", value, want)
	}
}

func TestEvaluateRejectsCycle(t *testing.T) {
	node := &MatrixNode{Matrix: ColorMatrix{}}
	node.Input = node

	if _, _, err := Evaluate(node, []float64{0, 0, 0, 1}); err == nil {
		t.Fatal("Evaluate accepted a cyclic graph")
	}
}

func TestEvaluateValidatesGraphAndSegments(t *testing.T) {
	if _, _, err := Evaluate(nil, []float64{0, 0, 0, 1}); err == nil {
		t.Fatal("Evaluate accepted a nil root")
	}
	if _, _, err := Evaluate(NewInputNode(0), []float64{0, 0, 0}); err == nil {
		t.Fatal("Evaluate accepted an input length that is not a multiple of four")
	}

	badSegments := []PowerSegment{
		{InputMin: 0, InputMax: 0.5, OutputMin: 0, OutputMax: 0.5, Gamma: 1},
		{InputMin: 0.6, InputMax: 1, OutputMin: 0.5, OutputMax: 1, Gamma: 1},
	}
	if _, _, err := Evaluate(NewPowerNode(NewInputNode(0), badSegments), []float64{0, 0, 0, 1}); err == nil {
		t.Fatal("Evaluate accepted non-contiguous power segments")
	}
}

func assertFiniteDifference(t *testing.T, root Node, inputs []float64, delta, tolerance float64) {
	t.Helper()

	_, jacobian, err := Evaluate(root, inputs)
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	numeric := zeroJacobian(4, len(inputs))

	for component := range inputs {
		perturbed := append([]float64(nil), inputs...)
		perturbed[component] -= delta
		minus, _, err := Evaluate(root, perturbed)
		if err != nil {
			t.Fatalf("minus Evaluate returned error: %v", err)
		}

		perturbed[component] = inputs[component] + delta
		plus, _, err := Evaluate(root, perturbed)
		if err != nil {
			t.Fatalf("plus Evaluate returned error: %v", err)
		}

		for row := 0; row < 4; row++ {
			numeric[row][component] = (plus[row] - minus[row]) / (2 * delta)
		}
	}

	assertMatrixClose(t, jacobian, numeric, tolerance)
}

func assertMatrixClose(t *testing.T, got, want [][]float64, tolerance float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("matrix row count = %d, want %d", len(got), len(want))
	}
	for row := range want {
		if len(got[row]) != len(want[row]) {
			t.Fatalf("row %d column count = %d, want %d", row, len(got[row]), len(want[row]))
		}
		for column := range want[row] {
			if !closeEnough(got[row][column], want[row][column], tolerance) {
				t.Fatalf("Jacobian[%d][%d] = %.10f, want %.10f", row, column, got[row][column], want[row][column])
			}
		}
	}
}

func rgbaClose(got, want RGBA, tolerance float64) bool {
	for i := range got {
		if !closeEnough(got[i], want[i], tolerance) {
			return false
		}
	}
	return true
}

func closeEnough(got, want, tolerance float64) bool {
	scale := math.Max(1, math.Abs(want))
	return math.Abs(got-want) <= tolerance*scale
}
