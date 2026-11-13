package netsec

import (
	"fmt"
	"math"
)

// BaseModel is an immutable, validated copy of the host's base manifest.
type BaseModel struct {
	modelDigest Digest
	layers      map[string]BaseLayer
	ordered     []BaseLayer
}

// NewBaseModel validates and copies a host-fixed base manifest.
func NewBaseModel(manifest BaseModelManifest) (*BaseModel, error) {
	if manifest.ModelDigest == "" {
		return nil, fmt.Errorf("%w: empty model digest", ErrInvalidConfig)
	}
	if len(manifest.Layers) == 0 {
		return nil, fmt.Errorf("%w: base model has no layers", ErrInvalidConfig)
	}

	model := &BaseModel{
		modelDigest: manifest.ModelDigest,
		layers:      make(map[string]BaseLayer, len(manifest.Layers)),
		ordered:     append([]BaseLayer(nil), manifest.Layers...),
	}
	for _, layer := range manifest.Layers {
		if layer.ID == "" {
			return nil, fmt.Errorf("%w: base layer has empty stable ID", ErrInvalidConfig)
		}
		if _, exists := model.layers[layer.ID]; exists {
			return nil, fmt.Errorf("%w: base layer %q", ErrDuplicateLayer, layer.ID)
		}
		if layer.InputDim <= 0 || layer.OutputDim <= 0 {
			return nil, fmt.Errorf("%w: base layer %q has non-positive dimensions", ErrInvalidConfig, layer.ID)
		}
		if int64(layer.InputDim) > math.MaxInt64/int64(layer.OutputDim) {
			return nil, fmt.Errorf("%w: base layer %q dimensions overflow", ErrInvalidConfig, layer.ID)
		}
		if _, ok := layer.DType.elementSize(); !ok {
			return nil, fmt.Errorf("%w: base layer %q has unsupported dtype %q", ErrInvalidConfig, layer.ID, layer.DType)
		}
		if layer.WeightDigest == "" {
			return nil, fmt.Errorf("%w: base layer %q has empty weight digest", ErrInvalidConfig, layer.ID)
		}
		model.layers[layer.ID] = layer
	}
	return model, nil
}

// ModelDigest returns the exact host-fixed base model digest.
func (b *BaseModel) ModelDigest() Digest {
	return b.modelDigest
}

// Layer returns the unique base layer with the given stable ID.
func (b *BaseModel) Layer(id string) (BaseLayer, bool) {
	layer, ok := b.layers[id]
	return layer, ok
}

// Layers returns a copy of the validated, host-ordered layer list.
func (b *BaseModel) Layers() []BaseLayer {
	return append([]BaseLayer(nil), b.ordered...)
}
