package netsec

import (
	"errors"
	"fmt"
	"strings"
)

const (
	// ClusterWideAnnotation makes a sealed secret independent of namespace and name.
	ClusterWideAnnotation = "sealedsecrets.bitnami.com/cluster-wide"
	// NamespaceWideAnnotation binds a sealed secret to its namespace only.
	NamespaceWideAnnotation = "sealedsecrets.bitnami.com/namespace-wide"

	ClusterWideAnnotationKey   = ClusterWideAnnotation
	NamespaceWideAnnotationKey = NamespaceWideAnnotation
)

// Scope is the binding that was used as the encryption label.
type Scope uint8

const (
	StrictScope Scope = iota
	NamespaceWideScope
	ClusterWideScope

	Strict        = StrictScope
	NamespaceWide = NamespaceWideScope
	ClusterWide   = ClusterWideScope
)

func (s Scope) String() string {
	switch s {
	case StrictScope:
		return "strict"
	case NamespaceWideScope:
		return "namespace-wide"
	case ClusterWideScope:
		return "cluster-wide"
	default:
		return fmt.Sprintf("Scope(%d)", uint8(s))
	}
}

var (
	// ErrConflictingScopeAnnotations is returned when both scope annotations are enabled.
	ErrConflictingScopeAnnotations = errors.New("sealed secret has both cluster-wide and namespace-wide annotations")
	// ErrInvalidScopeAnnotation is returned for an annotation value that is not a boolean.
	ErrInvalidScopeAnnotation = errors.New("sealed secret has an invalid scope annotation")
	// ErrInvalidObject is returned when a label cannot be derived from the object identity.
	ErrInvalidObject = errors.New("sealed secret has an invalid namespace or name")
	// ErrDecryptionFailed deliberately covers label mismatches and ciphertext failures alike.
	ErrDecryptionFailed = errors.New("sealed secret decryption failed")
)

// SealedSecret contains only the fields needed by the controller's unseal path.
type SealedSecret struct {
	Namespace     string
	Name          string
	Annotations   map[string]string
	EncryptedData map[string]string
}

// Scope derives the scope from the annotations that are present on the object now.
func (s SealedSecret) Scope() (Scope, error) {
	return ScopeFromAnnotations(s.Annotations)
}

// GetScope is an alias for Scope.
func (s SealedSecret) GetScope() (Scope, error) {
	return s.Scope()
}

// ExpectedLabel returns the label that a valid decryption must have used.
func (s SealedSecret) ExpectedLabel() (string, error) {
	return DeriveLabel(s.Namespace, s.Name, s.Annotations)
}

// GetLabel is an alias for ExpectedLabel.
func (s SealedSecret) GetLabel() (string, error) {
	return s.ExpectedLabel()
}

// Label is an alias for ExpectedLabel.
func (s SealedSecret) Label() (string, error) {
	return s.ExpectedLabel()
}

// ScopeFromAnnotations derives a scope from the two scope annotations.
func ScopeFromAnnotations(annotations map[string]string) (Scope, error) {
	clusterWide, err := annotationEnabled(annotations, ClusterWideAnnotation)
	if err != nil {
		return StrictScope, err
	}
	namespaceWide, err := annotationEnabled(annotations, NamespaceWideAnnotation)
	if err != nil {
		return StrictScope, err
	}

	switch {
	case clusterWide && namespaceWide:
		return StrictScope, ErrConflictingScopeAnnotations
	case clusterWide:
		return ClusterWideScope, nil
	case namespaceWide:
		return NamespaceWideScope, nil
	default:
		return StrictScope, nil
	}
}

// DeriveLabel derives the expected encryption label from the current object.
func DeriveLabel(namespace, name string, annotations map[string]string) (string, error) {
	scope, err := ScopeFromAnnotations(annotations)
	if err != nil {
		return "", err
	}
	return LabelForScope(scope, namespace, name)
}

// LabelForScope returns the encryption label for an explicit scope.
func LabelForScope(scope Scope, namespace, name string) (string, error) {
	switch scope {
	case StrictScope:
		if namespace == "" || name == "" {
			return "", fmt.Errorf("%w: strict scope requires both namespace and name", ErrInvalidObject)
		}
		return namespace + "/" + name, nil
	case NamespaceWideScope:
		if namespace == "" {
			return "", fmt.Errorf("%w: namespace-wide scope requires a namespace", ErrInvalidObject)
		}
		return namespace, nil
	case ClusterWideScope:
		return "", nil
	default:
		return "", fmt.Errorf("%w: unknown scope %d", ErrInvalidObject, scope)
	}
}

func annotationEnabled(annotations map[string]string, key string) (bool, error) {
	value, present := annotations[key]
	if !present {
		return false, nil
	}

	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("%w: %s=%q", ErrInvalidScopeAnnotation, key, value)
	}
}
