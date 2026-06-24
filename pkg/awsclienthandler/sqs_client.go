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

package awsclienthandler

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

var (
	SQSClient *sqs.Client
)

// NewSQSClientFromConfig creates and returns an Amazon SQS client using the provided context and AWS configuration.
func NewSQSClientFromConfig(ctx context.Context, awsConfig *AWSConfig) (*sqs.Client, error) {
	logger := log.FromContext(ctx)
	if SQSClient != nil {
		return SQSClient, nil
	}

	cfg, err := getAWSConfig(ctx, awsConfig)
	if err != nil {
		return nil, err
	}

	sqsOptions := func(o *sqs.Options) {
		if awsConfig.Endpoint != "" {
			o.BaseEndpoint = aws.String(awsConfig.Endpoint)
		}
	}
	SQSClient = sqs.NewFromConfig(cfg, sqsOptions)
	logger.Info("SQS client initialized ...")
	return SQSClient, nil
}

// GetSQSClient returns the initialized Amazon SQS client instance.
func GetSQSClient() (*sqs.Client, error) {
	if SQSClient == nil {
		return nil, errors.New("SQS client not initialized yet")
	}
	return SQSClient, nil
}

type s3EventMessage struct {
	Records []s3EventRecord `json:"Records"`
}

type s3EventRecord struct {
	S3 s3EventData `json:"s3"`
}

type s3EventData struct {
	Bucket s3BucketInfo `json:"bucket"`
	Object s3ObjectInfo `json:"object"`
}

type s3BucketInfo struct {
	Name string `json:"name"`
}

type s3ObjectInfo struct {
	Key string `json:"key"`
}

// DrainSQSQueue receives messages from the queue and deletes only those whose
// S3 event matches the given bucket and prefix. Unrelated messages are left in
// the queue for other consumers.
// Returns true if any matching messages were found (used as a wake-up signal).
func DrainSQSQueue(ctx context.Context, sqsClient *sqs.Client, queueURL, bucket, prefix string) (bool, error) {
	logger := log.FromContext(ctx)
	hasMessages := false

	for {
		output, err := sqsClient.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
			QueueUrl:            aws.String(queueURL),
			MaxNumberOfMessages: 10,
			WaitTimeSeconds:     0,
		})
		if err != nil {
			return hasMessages, err
		}
		if len(output.Messages) == 0 {
			break
		}
		for _, msg := range output.Messages {
			if msg.Body == nil {
				continue
			}
			var event s3EventMessage
			if err := json.Unmarshal([]byte(*msg.Body), &event); err != nil {
				logger.Error(err, "failed to parse SQS message body, skipping", "messageId", *msg.MessageId)
				continue
			}

			matches := false
			for _, record := range event.Records {
				if record.S3.Bucket.Name == bucket && strings.HasPrefix(record.S3.Object.Key, prefix) {
					matches = true
					break
				}
			}

			if !matches {
				continue
			}

			hasMessages = true
			if _, err := sqsClient.DeleteMessage(ctx, &sqs.DeleteMessageInput{
				QueueUrl:      aws.String(queueURL),
				ReceiptHandle: msg.ReceiptHandle,
			}); err != nil {
				logger.Error(err, "failed to delete SQS message", "messageId", *msg.MessageId)
			}
		}
	}
	return hasMessages, nil
}
