// Package v1alpha1 holds the okf.dev/v1alpha1 API: KnowledgeBase and Source.
// +kubebuilder:object:generate=true
// +groupName=okf.dev
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	// GroupVersion is the API group and version of these kinds.
	GroupVersion = schema.GroupVersion{Group: "okf.dev", Version: "v1alpha1"}

	// SchemeBuilder registers the kinds with a scheme.
	SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)

	// AddToScheme adds the kinds to a scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

func addKnownTypes(s *runtime.Scheme) error {
	s.AddKnownTypes(GroupVersion, &KnowledgeBase{}, &KnowledgeBaseList{}, &Source{}, &SourceList{})
	metav1.AddToGroupVersion(s, GroupVersion)
	return nil
}
