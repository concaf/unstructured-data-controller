/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllerutils

import (
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	operatorv1alpha1 "github.com/redhat-data-and-ai/unstructured-data-controller/api/v1alpha1"
)

const (
	// Default number of concurrent reconcile workers per controller.
	// Set to 5 based on production patterns from Crossplane (10), Flux (2-4),
	// and sizing heuristic: (expected CRs) / (avg reconcile latency in seconds).
	DefaultReconcilerConcurrency = 5

	apiGroup = "operator.dataverse.redhat.com"
)

// IsAlreadyReconciled returns true when the CR's spec has already been
// successfully reconciled. This allows controllers to skip redundant work
// during pod restarts when the informer re-lists all existing objects.
func IsAlreadyReconciled(generation, lastAppliedGeneration int64, conditions []metav1.Condition, conditionType string) bool {
	if lastAppliedGeneration != generation {
		return false
	}
	condition := meta.FindStatusCondition(conditions, conditionType)
	return condition != nil && condition.Status == metav1.ConditionTrue
}

// BuildGroupKindConcurrency builds a GroupKindConcurrency map from the
// ControllerConfig's reconcilerConcurrency settings. The map keys use the
// "Kind.group" format required by controller-runtime's config.Controller.
func BuildGroupKindConcurrency(reconcilerConcurrency *operatorv1alpha1.ReconcilerConcurrency) map[string]int {
	groupKindConcurrency := map[string]int{
		"UnstructuredDataPipeline." + apiGroup:  DefaultReconcilerConcurrency,
		"DocumentProcessor." + apiGroup:         DefaultReconcilerConcurrency,
		"ChunksGenerator." + apiGroup:           DefaultReconcilerConcurrency,
		"VectorEmbeddingsGenerator." + apiGroup: DefaultReconcilerConcurrency,
		"SourceCrawler." + apiGroup:             DefaultReconcilerConcurrency,
		"DestinationSyncer." + apiGroup:         DefaultReconcilerConcurrency,
	}

	if reconcilerConcurrency == nil {
		return groupKindConcurrency
	}

	// Override defaults with user-specified values.
	overrides := map[string]*int{
		"UnstructuredDataPipeline." + apiGroup:  reconcilerConcurrency.UnstructuredDataPipeline,
		"DocumentProcessor." + apiGroup:         reconcilerConcurrency.DocumentProcessor,
		"ChunksGenerator." + apiGroup:           reconcilerConcurrency.ChunksGenerator,
		"VectorEmbeddingsGenerator." + apiGroup: reconcilerConcurrency.VectorEmbeddingsGenerator,
		"SourceCrawler." + apiGroup:             reconcilerConcurrency.SourceCrawler,
		"DestinationSyncer." + apiGroup:         reconcilerConcurrency.DestinationSyncer,
	}
	for groupKind, concurrency := range overrides {
		if concurrency != nil {
			groupKindConcurrency[groupKind] = ptr.Deref(concurrency, DefaultReconcilerConcurrency)
		}
	}

	return groupKindConcurrency
}
