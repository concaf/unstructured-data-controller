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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	operatorv1alpha1 "github.com/redhat-data-and-ai/unstructured-data-controller/api/v1alpha1"
	"github.com/redhat-data-and-ai/unstructured-data-controller/internal/controller/controllerutils"
	"github.com/redhat-data-and-ai/unstructured-data-controller/pkg/awsclienthandler"
	"github.com/redhat-data-and-ai/unstructured-data-controller/pkg/docling"
	"github.com/redhat-data-and-ai/unstructured-data-controller/pkg/langchain"
)

var (
	ingestionBucket string

	doclingClient                          *docling.Client
	langchainClient                        *langchain.Client
	unstructuredSecret                     *corev1.Secret
	UnstructuredDataPipelineResyncInterval *int
)

type Model string
type ModelCredentials struct {
	Endpoint string
	APIKey   string
}

var modelMap = map[Model]ModelCredentials{
	Model("nomic-ai/nomic-embed-text-v1.5"): {
		Endpoint: "NOMIC_ENDPOINT",
		APIKey:   "NOMIC_API_KEY",
	},
}

// ControllerConfigReconciler reconciles a ControllerConfig object
type ControllerConfigReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=operator.dataverse.redhat.com,namespace=unstructured-controller-namespace,resources=controllerconfigs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=operator.dataverse.redhat.com,namespace=unstructured-controller-namespace,resources=controllerconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=operator.dataverse.redhat.com,namespace=unstructured-controller-namespace,resources=controllerconfigs/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the ControllerConfig object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.21.0/pkg/reconcile
func (r *ControllerConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info(fmt.Sprintf("Reconciling controller config %s", req.NamespacedName))

	configList := operatorv1alpha1.ControllerConfigList{}
	if err := r.List(ctx, &configList); err != nil {
		return ctrl.Result{}, err
	}

	if len(configList.Items) == 0 {
		logger.Info("no ControllerConfig found, nothing to reconcile")
		return ctrl.Result{}, nil
	}

	config := configList.Items[0]

	// Always set globals from config so they are populated on every Reconcile (including after process restart).
	ingestionBucket = config.Spec.UnstructuredDataProcessingConfig.IngestionBucket
	dataStorageBucket = config.Spec.UnstructuredDataProcessingConfig.DataStorageBucket
	cacheDirectory = config.Spec.UnstructuredDataProcessingConfig.CacheDirectory

	unstructuredSecret = &corev1.Secret{}
	if config.Spec.UnstructuredSecret != "" {
		if err := r.Get(ctx,
			types.NamespacedName{Name: config.Spec.UnstructuredSecret, Namespace: req.Namespace}, unstructuredSecret); err != nil {
			logger.Error(err, fmt.Sprintf("error fetching AWS secret %s, retrying in 10 seconds ", config.Spec.UnstructuredSecret))
			return ctrl.Result{
				Requeue:      true,
				RequeueAfter: 10 * time.Second,
			}, nil
		}
	}

	doclingServeURL := config.Spec.UnstructuredDataProcessingConfig.DoclingServeURL

	doclingConfig := &docling.ClientConfig{
		URL:                   doclingServeURL,
		MaxConcurrentRequests: int64(config.Spec.UnstructuredDataProcessingConfig.MaxConcurrentDoclingTasks),
	}

	doclingKey := string(unstructuredSecret.Data["DOCLING_USER_KEY"])
	if doclingKey != "" {
		doclingConfig.Key = doclingKey
	}

	// initialize the docling client
	doclingClient = docling.NewClientFromURL(doclingConfig)

	langchainClient = langchain.NewClient(
		langchain.ClientConfig{
			MaxConcurrentRequests: int64(config.Spec.UnstructuredDataProcessingConfig.MaxConcurrentLangchainTasks),
		},
	)

	logger.Info(fmt.Sprintf("Ingestion bucket: %s, Data storage bucket: %s, Cache directory: %s",
		ingestionBucket, dataStorageBucket, cacheDirectory))

	sourceAwsEndpoint := string(unstructuredSecret.Data["SOURCE_AWS_ENDPOINT"])
	awsConfig := awsclienthandler.AWSConfig{
		Region:          string(unstructuredSecret.Data["SOURCE_AWS_REGION"]),
		AccessKeyID:     string(unstructuredSecret.Data["SOURCE_AWS_ACCESS_KEY_ID"]),
		SecretAccessKey: string(unstructuredSecret.Data["SOURCE_AWS_SECRET_ACCESS_KEY"]),
		SessionToken:    string(unstructuredSecret.Data["SOURCE_AWS_SESSION_TOKEN"]),
		Endpoint:        sourceAwsEndpoint,
	}
	if _, err := awsclienthandler.NewSQSClientFromConfig(ctx, &awsConfig); err != nil {
		return ctrl.Result{}, err
	}
	logger.Info("SQS client created ...")
	if err := awsclienthandler.NewSourceS3ClientFromConfig(ctx, &awsConfig); err != nil {
		return ctrl.Result{}, err
	}
	logger.Info("S3 client created ...")
	if _, err := awsclienthandler.NewPresignClient(ctx); err != nil {
		return ctrl.Result{}, err
	}
	logger.Info("Presign client created ...")

	destinationAwsEndpoint := string(unstructuredSecret.Data["DESTINATION_AWS_ENDPOINT"])
	destAwsConfig := awsclienthandler.AWSConfig{
		Region:          string(unstructuredSecret.Data["DESTINATION_AWS_REGION"]),
		AccessKeyID:     string(unstructuredSecret.Data["DESTINATION_AWS_ACCESS_KEY_ID"]),
		SecretAccessKey: string(unstructuredSecret.Data["DESTINATION_AWS_SECRET_ACCESS_KEY"]),
		SessionToken:    string(unstructuredSecret.Data["DESTINATION_AWS_SESSION_TOKEN"]),
		Endpoint:        destinationAwsEndpoint,
	}
	if err := awsclienthandler.NewDestinationS3ClientFromConfig(ctx, &destAwsConfig); err != nil {
		return ctrl.Result{}, err
	}
	logger.Info("Destination S3 client created ...")

	fileStoreAwsEndpoint := string(unstructuredSecret.Data["FILE_STORE_AWS_ENDPOINT"])
	fileStoreAwsConfig := awsclienthandler.AWSConfig{
		Region:          string(unstructuredSecret.Data["FILE_STORE_AWS_REGION"]),
		AccessKeyID:     string(unstructuredSecret.Data["FILE_STORE_AWS_ACCESS_KEY_ID"]),
		SecretAccessKey: string(unstructuredSecret.Data["FILE_STORE_AWS_SECRET_ACCESS_KEY"]),
		SessionToken:    string(unstructuredSecret.Data["FILE_STORE_AWS_SESSION_TOKEN"]),
		Endpoint:        fileStoreAwsEndpoint,
	}
	if err := awsclienthandler.NewFileStoreS3ClientFromConfig(ctx, &fileStoreAwsConfig); err != nil {
		return ctrl.Result{}, err
	}
	logger.Info("File store S3 client created ...")

	// set the value of resync interval
	if config.Spec.UnstructuredDataProcessingConfig.UnstructuredDataPipelineResyncInterval != nil {
		UnstructuredDataPipelineResyncInterval = config.Spec.UnstructuredDataProcessingConfig.UnstructuredDataPipelineResyncInterval
		logger.Info("setting unstructured data pipeline resync interval to ", "minutes", *UnstructuredDataPipelineResyncInterval)
	}

	// Skip only status update if we've already processed this generation successfully
	if config.Status.LastAppliedGeneration == config.Generation && config.IsHealthy() {
		logger.Info("config already reconciled for current generation, skipping status update")
		return ctrl.Result{}, nil
	}

	// update the status of the Config CR to indicate that it is healthy
	if err := controllerutils.StatusPatch(ctx, r.Client, &config, func() {
		config.UpdateStatus(nil)
	}); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("successfully updated controllerConfig CR status", "status", config.Status)

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ControllerConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	labelPredicate := controllerutils.ForceReconcilePredicate()
	return ctrl.NewControllerManagedBy(mgr).
		For(&operatorv1alpha1.ControllerConfig{}).
		WithEventFilter(predicate.Or(predicate.GenerationChangedPredicate{}, labelPredicate)).
		Named("controllerconfig").
		Complete(r)
}
