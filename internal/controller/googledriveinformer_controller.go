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

package controller

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/api/drive/v3"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	operatorv1alpha1 "github.com/redhat-data-and-ai/unstructured-data-controller/api/v1alpha1"
	"github.com/redhat-data-and-ai/unstructured-data-controller/internal/controller/controllerutils"
	"github.com/redhat-data-and-ai/unstructured-data-controller/pkg/googledrive"
)

const (
	GoogleDriveInformerControllerName = "GoogleDriveInformer"
	driveInformerRequeueDuration      = 15 * time.Second
)

// driveServiceEntry holds a cached Drive service and the expanded set of all
// folder IDs (including recursive subfolders and shortcut targets).
type driveServiceEntry struct {
	service   *drive.Service
	folderIDs map[string]bool
}

// GoogleDriveInformerReconciler reconciles a GoogleDriveInformer object
type GoogleDriveInformerReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	driveServices map[string]*driveServiceEntry
}

// +kubebuilder:rbac:groups=operator.dataverse.redhat.com,namespace=unstructured-controller-namespace,resources=googledriveinformers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=operator.dataverse.redhat.com,namespace=unstructured-controller-namespace,resources=googledriveinformers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=operator.dataverse.redhat.com,namespace=unstructured-controller-namespace,resources=googledriveinformers/finalizers,verbs=update
// +kubebuilder:rbac:groups="",namespace=unstructured-controller-namespace,resources=secrets,verbs=get;list;watch

func (r *GoogleDriveInformerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("reconciling", "controller", GoogleDriveInformerControllerName)

	cr := &operatorv1alpha1.GoogleDriveInformer{}
	if err := r.Get(ctx, req.NamespacedName, cr); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	crKey := client.ObjectKeyFromObject(cr)
	if err := controllerutils.StatusUpdateWithRetry(ctx, r.Client, crKey, func() client.Object { return &operatorv1alpha1.GoogleDriveInformer{} }, func(obj client.Object) {
		obj.(*operatorv1alpha1.GoogleDriveInformer).SetWaiting()
	}); err != nil {
		logger.Error(err, "failed to update GoogleDriveInformer status")
		return ctrl.Result{}, err
	}

	// get or create drive service and expanded folder IDs
	entry, err := r.getDriveServiceEntry(ctx, cr)
	if err != nil {
		return r.handleError(ctx, cr, err)
	}

	// if pageToken is empty, get the start page token
	pageToken := cr.Status.PageToken
	if pageToken == "" {
		resp, err := entry.service.Changes.GetStartPageToken().Context(ctx).Do()
		if err != nil {
			return r.handleError(ctx, cr, err)
		}
		logger.Info("obtained start page token", "token", resp.StartPageToken)

		if err := controllerutils.StatusUpdateWithRetry(ctx, r.Client, crKey, func() client.Object { return &operatorv1alpha1.GoogleDriveInformer{} }, func(obj client.Object) {
			obj.(*operatorv1alpha1.GoogleDriveInformer).Status.PageToken = resp.StartPageToken
		}); err != nil {
			logger.Error(err, "failed to save start page token")
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, nil
	}

	// poll for changes
	changeList, err := entry.service.Changes.List(pageToken).
		Fields("newStartPageToken", "changes(fileId,file(id,parents,trashed))").
		Context(ctx).
		Do()
	if err != nil {
		return r.handleError(ctx, cr, err)
	}

	// check if any change is in a watched folder (including subfolders and shortcut targets)
	shouldTrigger := false
	for _, change := range changeList.Changes {
		if change.File == nil {
			continue
		}
		if change.File.Trashed {
			continue
		}
		for _, parent := range change.File.Parents {
			if entry.folderIDs[parent] {
				shouldTrigger = true
				logger.Info("detected change in watched folder", "fileId", change.FileId, "parent", parent)
				break
			}
		}
		if shouldTrigger {
			break
		}
	}

	// trigger the UnstructuredDataProduct if changes detected
	if shouldTrigger {
		udpKey := client.ObjectKey{Namespace: req.Namespace, Name: cr.Spec.DataProduct}
		if err := controllerutils.AddForceReconcileLabelWithRetry(ctx, r.Client, udpKey, func() client.Object { return &operatorv1alpha1.UnstructuredDataProduct{} }); err != nil {
			logger.Error(err, "failed to add force reconcile label to UnstructuredDataProduct", "dataProduct", cr.Spec.DataProduct)
			return r.handleError(ctx, cr, err)
		}
		logger.Info("triggered UnstructuredDataProduct reconciliation", "dataProduct", cr.Spec.DataProduct)
	}

	// save the new page token
	newPageToken := changeList.NewStartPageToken
	if newPageToken == "" {
		newPageToken = pageToken
	}
	if err := controllerutils.StatusUpdateWithRetry(ctx, r.Client, crKey, func() client.Object { return &operatorv1alpha1.GoogleDriveInformer{} }, func(obj client.Object) {
		obj.(*operatorv1alpha1.GoogleDriveInformer).Status.PageToken = newPageToken
	}); err != nil {
		logger.Error(err, "failed to save page token")
		return ctrl.Result{}, err
	}

	// remove force-reconcile label from self
	if err := controllerutils.RemoveForceReconcileLabelWithRetry(ctx, r.Client, crKey, func() client.Object { return &operatorv1alpha1.GoogleDriveInformer{} }); err != nil {
		logger.Error(err, "error removing the force-reconcile label from the GoogleDriveInformer CR")
		return ctrl.Result{}, err
	}

	successMessage := "successfully polled for changes, will check again ..."
	if err := controllerutils.StatusUpdateWithRetry(ctx, r.Client, crKey, func() client.Object { return &operatorv1alpha1.GoogleDriveInformer{} }, func(obj client.Object) {
		obj.(*operatorv1alpha1.GoogleDriveInformer).UpdateStatus(successMessage, nil)
	}); err != nil {
		logger.Error(err, "failed to update GoogleDriveInformer status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{
		Requeue:      true,
		RequeueAfter: driveInformerRequeueDuration,
	}, nil
}

func (r *GoogleDriveInformerReconciler) getDriveServiceEntry(ctx context.Context, cr *operatorv1alpha1.GoogleDriveInformer) (*driveServiceEntry, error) {
	logger := log.FromContext(ctx)
	cacheKey := cr.Namespace + "/" + cr.Spec.Secret

	if entry, ok := r.driveServices[cacheKey]; ok {
		return entry, nil
	}

	// read the secret
	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: cr.Namespace, Name: cr.Spec.Secret}, secret); err != nil {
		return nil, err
	}

	saJSON, ok := secret.Data["credentials.json"]
	if !ok {
		return nil, fmt.Errorf("secret %s/%s does not contain key 'credentials.json'", cr.Namespace, cr.Spec.Secret)
	}

	srv, err := googledrive.NewDriveService(ctx, saJSON)
	if err != nil {
		return nil, err
	}

	// collect all folder IDs recursively (subfolders + shortcut targets)
	rootFolderIDs := make([]string, len(cr.Spec.Folders))
	for i, f := range cr.Spec.Folders {
		rootFolderIDs[i] = f.ID
	}

	allFolderIDs, err := googledrive.CollectAllFolderIDs(ctx, srv, rootFolderIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to collect folder IDs: %w", err)
	}
	logger.Info("cached drive service and folder IDs", "cacheKey", cacheKey, "folderCount", len(allFolderIDs))

	entry := &driveServiceEntry{
		service:   srv,
		folderIDs: allFolderIDs,
	}

	if r.driveServices == nil {
		r.driveServices = make(map[string]*driveServiceEntry)
	}
	r.driveServices[cacheKey] = entry
	return entry, nil
}

func (r *GoogleDriveInformerReconciler) handleError(ctx context.Context, cr *operatorv1alpha1.GoogleDriveInformer, err error) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Error(err, "encountered error")
	reconcileErr := err
	crKey := client.ObjectKeyFromObject(cr)
	if updateErr := controllerutils.StatusUpdateWithRetry(ctx, r.Client, crKey, func() client.Object { return &operatorv1alpha1.GoogleDriveInformer{} }, func(obj client.Object) {
		obj.(*operatorv1alpha1.GoogleDriveInformer).UpdateStatus("", reconcileErr)
	}); updateErr != nil {
		logger.Error(updateErr, "failed to update GoogleDriveInformer status")
		return ctrl.Result{}, updateErr
	}
	return ctrl.Result{}, reconcileErr
}

// SetupWithManager sets up the controller with the Manager.
func (r *GoogleDriveInformerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	labelPredicate := controllerutils.ForceReconcilePredicate()
	return ctrl.NewControllerManagedBy(mgr).
		For(&operatorv1alpha1.GoogleDriveInformer{}).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(
			func(ctx context.Context, obj client.Object) []reconcile.Request {
				// invalidate cached drive service for this secret
				cacheKey := obj.GetNamespace() + "/" + obj.GetName()
				delete(r.driveServices, cacheKey)

				// list all GoogleDriveInformer CRs in the same namespace
				var informerList operatorv1alpha1.GoogleDriveInformerList
				if err := mgr.GetClient().List(ctx, &informerList, client.InNamespace(obj.GetNamespace())); err != nil {
					return nil
				}
				var requests []reconcile.Request
				for _, informer := range informerList.Items {
					if informer.Spec.Secret == obj.GetName() {
						requests = append(requests, reconcile.Request{
							NamespacedName: client.ObjectKeyFromObject(&informer),
						})
					}
				}
				return requests
			},
		), builder.WithPredicates(predicate.ResourceVersionChangedPredicate{})).
		WithEventFilter(predicate.Or(predicate.GenerationChangedPredicate{}, labelPredicate)).
		Named("googledriveinformer").
		Complete(r)
}
