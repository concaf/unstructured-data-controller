//go:build e2e
// +build e2e

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

package e2e

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/redhat-data-and-ai/unstructured-data-controller/api/v1alpha1"
	"github.com/redhat-data-and-ai/unstructured-data-controller/pkg/awsclienthandler"
	operatorUtils "github.com/redhat-data-and-ai/unstructured-data-controller/test/utils"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimachinerywait "k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/e2e-framework/klient"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
)

func TestUnstructuredDataLoad(t *testing.T) {
	feature := features.New("Unstructured Data Load")

	unstructuredBucketName := "unstructured-bucket"
	unstructuredDataStorageBucketName := "data-storage-bucket"
	outputChunksBucketName := "output-chunks-bucket"
	unstructuredQueueName := "unstructured-queue"

	schemaName := "unstructured"
	dataProductCRName := schemaName

	queueURL := "http://sqs.us-east-1.localhost.localstack.cloud:4566/000000000000/" + unstructuredQueueName
	unstructuredFilesDirectory := "test/resources/unstructured/unstructured-files"

	//clients
	secret := &v1.Secret{}
	var kubeClient klient.Client

	feature.Setup(
		func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			kubeClient = cfg.Client()

			err := v1alpha1.AddToScheme(kubeClient.Resources(testNamespace).GetScheme())
			if err != nil {
				t.Fatalf("Failed to add scheme: %s", err)
			}

			// get key secret
			if err := kubeClient.Resources().Get(ctx, unstructuredSecretName, testNamespace, secret); err != nil {
				t.Fatalf("Failed to get secret: %s", err)
			}

			// create AWS clients
			err = awsclienthandler.NewSourceS3ClientFromConfig(ctx, &awsclienthandler.AWSConfig{
				Region:          "us-east-1",
				AccessKeyID:     "test",
				SecretAccessKey: "test",
				Endpoint:        localstackURL,
			})
			if err != nil {
				t.Fatal(err)
			}

			// create SQS client
			sqsClient, err := awsclienthandler.NewSQSClientFromConfig(ctx, &awsclienthandler.AWSConfig{
				Region:          "us-east-1",
				AccessKeyID:     "test",
				SecretAccessKey: "test",
				Endpoint:        localstackURL,
			})
			if err != nil {
				t.Fatal(err)
			}

			s3Client, err := awsclienthandler.GetSourceS3Client()
			if err != nil {
				t.Fatal(err)
			}
			// create unstructured bucket
			_, err = s3Client.CreateBucket(ctx, &s3.CreateBucketInput{
				Bucket: aws.String(unstructuredBucketName),
			})
			if err != nil {
				t.Fatal(err)
			}

			// create unstructured data storage bucket
			_, err = s3Client.CreateBucket(ctx, &s3.CreateBucketInput{
				Bucket: aws.String(unstructuredDataStorageBucketName),
			})
			if err != nil {
				t.Fatal(err)
			}

			// create output chunks bucket
			_, err = s3Client.CreateBucket(ctx, &s3.CreateBucketInput{
				Bucket: aws.String(outputChunksBucketName),
			})
			if err != nil {
				t.Fatal(err)
			}

			// create SQS queue
			_, err = sqsClient.CreateQueue(ctx, &sqs.CreateQueueInput{
				QueueName: aws.String(unstructuredQueueName),
			})
			if err != nil {
				t.Fatal(err)
			}

			// create S3 --> SQS notification integration
			_, err = s3Client.PutBucketNotificationConfiguration(ctx, &s3.PutBucketNotificationConfigurationInput{
				Bucket: aws.String(unstructuredBucketName),
				NotificationConfiguration: &types.NotificationConfiguration{
					QueueConfigurations: []types.QueueConfiguration{
						{
							QueueArn: aws.String("arn:aws:sqs:us-east-1:000000000000:" + unstructuredQueueName),
							Events:   []types.Event{types.EventS3ObjectCreated, types.EventS3ObjectRemoved},
						},
					},
				},
			})
			if err != nil {
				t.Fatal(err)
			}

			// create SQSInformer CR
			SQSInformer := &v1alpha1.SQSInformer{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-sqs-informer",
					Namespace: testNamespace,
				},
				Spec: v1alpha1.SQSInformerSpec{
					QueueURL: queueURL,
				},
			}
			err = kubeClient.Resources(testNamespace).Create(ctx, SQSInformer)
			if err != nil {
				t.Fatal(err)
			}

			// wait for SQSInformer CR to be ready
			if err := operatorUtils.WaitForResourceReady(ctx, v1alpha1.SQSInformerCondition, "SQSInformers.operator.dataverse.redhat.com", "test-sqs-informer", testNamespace); err != nil {
				t.Error(err)
			}

			// create unstructured data pipeline CR
			unstructuredDataPipeline := operatorUtils.GetUnstructuredDataPipelineResource(dataProductCRName, testNamespace)
			t.Log("create unstructured datapipeline CR ...")
			if err := kubeClient.Resources(testNamespace).Create(ctx, &unstructuredDataPipeline); err != nil {
				if !apierrors.IsAlreadyExists(err) {
					t.Fatal(err)
				}
			}

			// wait for unstructured data pipeline CR to be healthy
			t.Log("wait for unstructured data pipeline CR to be healthy")
			if err := operatorUtils.WaitForResourceReady(ctx, v1alpha1.UnstructuredDataPipelineCondition, "unstructureddatapipelines.operator.dataverse.redhat.com", dataProductCRName, testNamespace); err != nil {
				t.Error(err)
			}
			t.Log("unstructured data pipeline CR is healthy")

			return ctx
		},
	)

	feature.Assess("upload files to unstructured bucket and verify they land in output chunks bucket", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		// create AWS clients for file operations
		err := awsclienthandler.NewSourceS3ClientFromConfig(ctx, &awsclienthandler.AWSConfig{
			Region:          "us-east-1",
			AccessKeyID:     "test",
			SecretAccessKey: "test",
			Endpoint:        localstackURL,
		})
		if err != nil {
			t.Error(err)
		}

		// get all files in the directory
		files, err := os.ReadDir(unstructuredFilesDirectory)
		if err != nil {
			t.Error(err)
		}

		if len(files) == 0 {
			t.Error("no files found in the directory")
		}

		s3Client, err := awsclienthandler.GetSourceS3Client()
		if err != nil {
			t.Error(err)
		}

		// upload files to unstructured S3 bucket
		for _, file := range files {
			if file.IsDir() {
				t.Errorf("subdirectories are not allowed in the unstructured test files directory: %s", unstructuredFilesDirectory)
			}

			fileContent, err := os.ReadFile(filepath.Join(unstructuredFilesDirectory, file.Name()))
			if err != nil {
				t.Error(err)
			}

			key := fmt.Sprintf("%s/%s", schemaName, file.Name())
			_, err = s3Client.PutObject(ctx, &s3.PutObjectInput{
				Bucket: aws.String(unstructuredBucketName),
				Key:    aws.String(key),
				Body:   bytes.NewReader(fileContent),
			})
			if err != nil {
				t.Error(err)
			}
			t.Logf("uploaded test file: %s", key)
		}

		// wait for files to be processed and appear in the output chunks bucket
		t.Log("wait for files to be processed and appear in output chunks bucket ...")

		var outputFiles []string
		if err := apimachinerywait.PollUntilContextTimeout(
			context.Background(),
			5*time.Second,
			10*time.Minute,
			false,
			func(ctx context.Context) (done bool, err error) {
				output, err := s3Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
					Bucket: aws.String(outputChunksBucketName),
					Prefix: aws.String(schemaName + "/"),
				})
				if err != nil {
					t.Logf("error listing output chunks bucket: %v, retrying ...", err)
					return false, nil
				}
				if len(output.Contents) < len(files) {
					t.Logf("expected at least %d files in output chunks bucket, got %d, retrying ...", len(files), len(output.Contents))
					return false, nil
				}
				outputFiles = make([]string, 0, len(output.Contents))
				for _, obj := range output.Contents {
					outputFiles = append(outputFiles, *obj.Key)
				}
				return true, nil
			},
		); err != nil {
			t.Error(err)
		}

		t.Logf("output chunk files: %+v", outputFiles)

		// make sure all the source files have corresponding output in the chunks bucket
		for _, file := range files {
			found := false
			expectedPrefix := fmt.Sprintf("%s/%s", schemaName, file.Name())
			for _, outputFile := range outputFiles {
				if len(outputFile) >= len(expectedPrefix) && outputFile[:len(expectedPrefix)] == expectedPrefix {
					t.Logf("file %s processed successfully", file.Name())
					found = true
					break
				}
			}
			if !found {
				t.Errorf("file %s not found in output chunks bucket (expected prefix: %s)", file.Name(), expectedPrefix)
			}
		}

		return ctx
	})

	feature.Assess("Deletion of file from the bucket and verifying removal from output chunks bucket", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		// create a new s3 client
		t.Log("Creating s3 client ...")
		err := awsclienthandler.NewSourceS3ClientFromConfig(ctx, &awsclienthandler.AWSConfig{
			Region:          "us-east-1",
			AccessKeyID:     "test",
			SecretAccessKey: "test",
			Endpoint:        localstackURL,
		})
		if err != nil {
			t.Error(err)
		}

		s3Client, err := awsclienthandler.GetSourceS3Client()
		if err != nil {
			t.Error(err)
		}

		// list all the files in the unstructured bucket as we have ingested files in the last step
		t.Log("Listing objects from unstructured bucket ...")
		output, err := s3Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket: aws.String(unstructuredBucketName),
			Prefix: aws.String(schemaName + "/"),
		})
		if err != nil {
			t.Errorf("Unable to list objects from the unstructured bucket: %s", err)
		}

		// Store the file name in a slice - filesinBucket
		filesinBucket := []string{}
		for _, file := range output.Contents {
			t.Logf("file: %s", *file.Key)
			filesinBucket = append(filesinBucket, *file.Key)
		}

		// verify the count is at least 1
		if len(filesinBucket) == 0 {
			t.Error("Unable to list file from the bucket")
		}

		// store the file name at 0th index of the slice in a variable - fileToDelete
		fileToDelete := filesinBucket[0]

		// delete file from the bucket on the 0th index of the slice
		_, err = s3Client.DeleteObject(ctx, &s3.DeleteObjectInput{
			Bucket: aws.String(unstructuredBucketName),
			Key:    aws.String(fileToDelete),
		})
		if err != nil {
			t.Errorf("Unable to delete file from the bucket: %s", err)
		}

		t.Logf("deleted file: %s", fileToDelete)

		// delete the 0th index element from the slice as well
		filesinBucket = filesinBucket[1:]

		// wait for 10 seconds
		t.Log("waiting for 10 seconds")
		time.Sleep(10 * time.Second)

		// wait for the deleted file to be removed from the output chunks bucket
		t.Log("wait for deleted file to be removed from output chunks bucket ...")

		if err := apimachinerywait.PollUntilContextTimeout(
			context.Background(),
			5*time.Second,
			10*time.Minute,
			false,
			func(ctx context.Context) (done bool, err error) {
				outputObjects, err := s3Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
					Bucket: aws.String(outputChunksBucketName),
					Prefix: aws.String(fileToDelete),
				})
				if err != nil {
					t.Logf("error listing output chunks bucket: %v, retrying ...", err)
					return false, nil
				}
				if len(outputObjects.Contents) > 0 {
					t.Logf("deleted file %s still present in output chunks bucket (%d objects), retrying ...", fileToDelete, len(outputObjects.Contents))
					return false, nil
				}
				return true, nil
			},
		); err != nil {
			t.Error(err)
		}

		t.Logf("deleted file %s is not present in the output chunks bucket", fileToDelete)

		return ctx
	})

	feature.Assess("Will change docling config and verify pipeline reconciles successfully", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		t.Log("Updating the docling config for the data product")
		doclingConfig := &v1alpha1.DoclingConfig{
			FromFormats:     []string{"pdf", "docx", "pptx", "xlsx"},
			ImageExportMode: "embedded",
			DoOCR:           true,
			ForceOCR:        false,
			OCREngine:       "easyocr",
			OCRLang:         []string{"en"},
			PDFBackend:      "dlparse_v4",
			TableMode:       "accurate",
		}

		// fetch the latest version of the unstructured data pipeline CR
		unstructuredDataPipelineCR := &v1alpha1.UnstructuredDataPipeline{}
		if err := kubeClient.Resources().Get(ctx, dataProductCRName, testNamespace, unstructuredDataPipelineCR); err != nil {
			t.Error(err)
		}
		unstructuredDataPipelineCR.Spec.DocumentProcessorConfig.DoclingConfig = *doclingConfig
		if err := kubeClient.Resources().WithNamespace(testNamespace).Update(ctx, unstructuredDataPipelineCR); err != nil {
			t.Error(err)
		}
		t.Log("Successfully updated the docling config in the unstructured data pipeline CR")

		// wait for the unstructured data pipeline CR to be ready
		if err := operatorUtils.WaitForResourceReady(ctx, v1alpha1.UnstructuredDataPipelineCondition, "unstructureddatapipelines.operator.dataverse.redhat.com", dataProductCRName, testNamespace); err != nil {
			t.Error(err)
		}

		t.Log("UnstructuredDataPipeline successfully reconciled")

		// wait for the document processor to be ready
		if err := operatorUtils.WaitForResourceReady(ctx, v1alpha1.DocumentProcessorCondition, "documentprocessors.operator.dataverse.redhat.com", dataProductCRName, testNamespace); err != nil {
			t.Error(err)
		}

		t.Log("DocumentProcessor successfully reconciled")

		// wait until the chunksgenerator CR is ready
		if err := operatorUtils.WaitForResourceReady(ctx, v1alpha1.ChunksGeneratorCondition, "chunksgenerators.operator.dataverse.redhat.com", dataProductCRName, testNamespace); err != nil {
			t.Error(err)
		}
		t.Log("ChunksGenerator successfully reconciled")

		// wait for the vector embeddings generator to be ready
		if err := operatorUtils.WaitForResourceReady(ctx, v1alpha1.VectorEmbeddingGenerationConditionType, "vectorembeddingsgenerators.operator.dataverse.redhat.com", dataProductCRName, testNamespace); err != nil {
			t.Error(err)
		}
		t.Log("VectorEmbeddingsGenerator successfully reconciled")

		if err := operatorUtils.WaitForResourceReady(ctx, v1alpha1.UnstructuredDataPipelineCondition, "unstructureddatapipelines.operator.dataverse.redhat.com", dataProductCRName, testNamespace); err != nil {
			t.Error(err)
		}
		t.Log("UnstructuredDataPipeline successfully reconciled after docling config update")

		return ctx
	})

	feature.Assess("Will change chunking config and verify pipeline reconciles successfully", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		// update the chunking config for the data product
		t.Log("Updating the chunking config for the data product")
		chunkingConfig := &v1alpha1.ChunksGeneratorConfig{
			Strategy: "markdownTextSplitter",
			MarkdownSplitterConfig: v1alpha1.MarkdownSplitterConfig{
				ChunkSize:        1500,
				ChunkOverlap:     300,
				CodeBlocks:       true,
				ReferenceLinks:   true,
				HeadingHierarchy: true,
				JoinTableRows:    true,
			},
		}

		// fetch the latest version of the unstructured data pipeline CR
		unstructuredDataPipelineCR := &v1alpha1.UnstructuredDataPipeline{}
		if err := kubeClient.Resources().Get(ctx, dataProductCRName, testNamespace, unstructuredDataPipelineCR); err != nil {
			t.Error(err)
		}
		unstructuredDataPipelineCR.Spec.ChunksGeneratorConfig = *chunkingConfig
		if err := kubeClient.Resources().WithNamespace(testNamespace).Update(ctx, unstructuredDataPipelineCR); err != nil {
			t.Error(err)
		}
		t.Log("Successfully updated the chunking config in the unstructured data pipeline CR")

		// wait until the unstructured data pipeline CR is ready
		if err := operatorUtils.WaitForResourceReady(ctx, v1alpha1.UnstructuredDataPipelineCondition, "unstructureddatapipelines.operator.dataverse.redhat.com", dataProductCRName, testNamespace); err != nil {
			t.Error(err)
		}
		t.Log("UnstructuredDataPipeline successfully reconciled")

		// wait for the document processor to be ready
		if err := operatorUtils.WaitForResourceReady(ctx, v1alpha1.DocumentProcessorCondition, "documentprocessors.operator.dataverse.redhat.com", dataProductCRName, testNamespace); err != nil {
			t.Error(err)
		}
		t.Log("DocumentProcessor successfully reconciled")

		// wait for the chunks generator to be ready
		if err := operatorUtils.WaitForResourceReady(ctx, v1alpha1.ChunksGeneratorCondition, "chunksgenerators.operator.dataverse.redhat.com", dataProductCRName, testNamespace); err != nil {
			t.Error(err)
		}
		t.Log("ChunksGenerator successfully reconciled")

		// wait for the vector embeddings generator to be ready
		if err := operatorUtils.WaitForResourceReady(ctx, v1alpha1.VectorEmbeddingGenerationConditionType, "vectorembeddingsgenerators.operator.dataverse.redhat.com", dataProductCRName, testNamespace); err != nil {
			t.Error(err)
		}
		t.Log("VectorEmbeddingsGenerator successfully reconciled")

		// now fetch unstructured data pipeline CR and wait until it is ready
		if err := operatorUtils.WaitForResourceReady(ctx, v1alpha1.UnstructuredDataPipelineCondition, "unstructureddatapipelines.operator.dataverse.redhat.com", dataProductCRName, testNamespace); err != nil {
			t.Error(err)
		}
		t.Log("UnstructuredDataPipeline successfully reconciled after chunking config update")

		return ctx
	})

	feature.Teardown(
		func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			// delete SQSInformer CR
			SQSInformer := &v1alpha1.SQSInformer{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-sqs-informer",
					Namespace: testNamespace,
				},
			}
			if err := kubeClient.Resources(testNamespace).Delete(ctx, SQSInformer); err != nil {
				t.Fatal(err)
			}

			// delete unstructured data pipeline CR
			unstructuredDataPipeline := &v1alpha1.UnstructuredDataPipeline{
				ObjectMeta: metav1.ObjectMeta{
					Name:      dataProductCRName,
					Namespace: testNamespace,
				},
			}
			if err := kubeClient.Resources(testNamespace).Delete(ctx, unstructuredDataPipeline); err != nil {
				t.Fatal(err)
			}
			return ctx
		},
	)

	testenv.Test(t, feature.Feature())
}
