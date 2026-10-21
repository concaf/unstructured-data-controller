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
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"

	operatorv1alpha1 "github.com/redhat-data-and-ai/unstructured-data-controller/api/v1alpha1"
)

// FilesProcessedGetter is implemented by all stage CRs to expose the filesProcessed counter.
type FilesProcessedGetter interface {
	GetFilesProcessed() int64
}

// FilesProcessedChangedPredicate filters Update events so that only changes
// to the filesProcessed status field trigger a reconcile.
type FilesProcessedChangedPredicate struct{}

func (FilesProcessedChangedPredicate) Create(_ event.CreateEvent) bool   { return true }
func (FilesProcessedChangedPredicate) Delete(_ event.DeleteEvent) bool   { return false }
func (FilesProcessedChangedPredicate) Generic(_ event.GenericEvent) bool { return false }
func (FilesProcessedChangedPredicate) Update(e event.UpdateEvent) bool {
	oldObj, ok1 := e.ObjectOld.(FilesProcessedGetter)
	newObj, ok2 := e.ObjectNew.(FilesProcessedGetter)
	if !ok1 || !ok2 {
		return true // can't compare, allow through
	}
	return oldObj.GetFilesProcessed() != newObj.GetFilesProcessed()
}

// IsAlreadyReconciled returns true when the CR's spec has already been
// successfully reconciled (generation matches and condition is healthy).
// Use inside Reconcile() to skip expensive work on pod restart while still
// returning RequeueAfter to restart polling loops. Do NOT use in predicates
// because predicates block the Create event entirely, preventing RequeueAfter
// from being re-established after a restart.
// StageCRName returns the Kubernetes resource name for a stage CR within a pipeline.
// All stage CRs follow the convention: pipelineName-stageName.
func StageCRName(pipelineName, stageName string) string {
	return pipelineName + "-" + stageName
}

func IsAlreadyReconciled(generation, lastAppliedGeneration int64, conditions []metav1.Condition, conditionType string) bool {
	if lastAppliedGeneration != generation {
		return false
	}
	condition := meta.FindStatusCondition(conditions, conditionType)
	return condition != nil && condition.Status == metav1.ConditionTrue
}

// HasUpstreamChanged returns true if the upstream stage produced new files
// since this controller last reconciled. Think of a pod restart as going home
// for the night: when you come back in the morning, you look at the current
// state of your desk (upstream filesProcessed) rather than relying on messages
// (watch events) that were sent while you were away.
func HasUpstreamChanged(currentUpstreamFiles, lastSeenUpstreamFiles int64) bool {
	return currentUpstreamFiles != lastSeenUpstreamFiles
}

// GetUpstreamFilesProcessed fetches the filesProcessed count from an upstream
// stage CR by trying each stage type. Only called in the IsAlreadyReconciled
// path (restart/no-op), so the cost of up to 5 Gets is acceptable compared to
// the hundreds of S3 calls a full reconcile would make.
func GetUpstreamFilesProcessed(ctx context.Context, c client.Client, name, namespace string) (int64, error) {
	key := client.ObjectKey{Name: name, Namespace: namespace}
	stages := []FilesProcessedGetter{
		&operatorv1alpha1.SourceCrawler{},
		&operatorv1alpha1.DocumentProcessor{},
		&operatorv1alpha1.ChunksGenerator{},
		&operatorv1alpha1.VectorEmbeddingsGenerator{},
		&operatorv1alpha1.DestinationSyncer{},
	}
	for _, stage := range stages {
		obj, ok := stage.(client.Object)
		if !ok {
			continue
		}
		if err := c.Get(ctx, key, obj); err != nil {
			continue
		}
		return stage.GetFilesProcessed(), nil
	}
	return 0, fmt.Errorf("no upstream stage found: %s/%s", namespace, name)
}
