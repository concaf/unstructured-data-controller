/*
Copyright 2025.

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

package unstructured

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/redhat-data-and-ai/unstructured-data-controller/pkg/awsclienthandler"
	"github.com/redhat-data-and-ai/unstructured-data-controller/pkg/filestore"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type DataSource interface {
	// SyncFilesToFilestore will store all files from the source to the filestore and return the list of file paths
	SyncFilesToFilestore(ctx context.Context, fs *filestore.FileStore) ([]RawFileMetadata, error)
}

type S3BucketSource struct {
	S3Client  *s3.Client
	Bucket    string
	Prefix    string
	OutputDir string
}

func (s *S3BucketSource) SyncFilesToFilestore(ctx context.Context, fs *filestore.FileStore) ([]RawFileMetadata, error) {
	logger := log.FromContext(ctx)
	logger.Info("listing objects in prefix", "bucket", s.Bucket, "prefix", s.Prefix)
	objects, err := awsclienthandler.ListObjectsInPrefix(ctx, s.S3Client, s.Bucket, s.Prefix)
	if err != nil {
		return nil, err
	}

	storedFiles := []RawFileMetadata{}
	errorList := map[string]error{}
	sourceFileMap := map[string]bool{}

	for _, object := range objects {
		// skip S3 folder marker objects (keys ending with "/") — storing these
		// as regular files would block directory creation for real files underneath
		if strings.HasSuffix(*object.Key, "/") {
			continue
		}
		file := RawFileMetadata{
			FilePath: s.filestorePath(*object.Key),
			UID:      *object.ETag,
		}
		logger.Info("storing file", "file", file.FilePath)
		sourceFileMap[file.FilePath] = true

		stored, err := s.storeFile(ctx, fs, &file)
		if err != nil {
			logger.Error(err, "failed to store file", "file", file.FilePath)
			errorList[file.FilePath] = err
			continue
		}
		if stored {
			logger.Info("successfully stored file", "file", file.FilePath)
			storedFiles = append(storedFiles, file)
		}
	}
	// Listing all the file in the local s3 filestore
	localFiles, err := fs.ListFilesInPath(ctx, s.outputPrefix())
	if err != nil {
		logger.Error(err, "failed to list files in filestore", "prefix", s.Prefix)
		return nil, err
	}

	// logic to delete files and its respective files if the file is removed from upstream bucket
	for _, localFilePath := range localFiles {
		rawFilePath := localFilePath
		if trimmed, ok := strings.CutSuffix(localFilePath, ".json"); ok {
			rawFilePath = trimmed
		}

		if _, exists := sourceFileMap[rawFilePath]; !exists {
			logger.Info("file or its parent does not exist in the source, deleting from the filestore", "file", localFilePath)
			if err := fs.Delete(ctx, localFilePath); err != nil {
				logger.Error(err, "failed to delete file from filestore", "file", localFilePath)
				errorList[localFilePath] = err
			} else {
				logger.Info("successfully deleted file from the filestore", "file", localFilePath)
			}
		}
	}

	errorMessage := ""
	for filePath, err := range errorList {
		errorMessage += fmt.Sprintf("file: %s, error: %v\n", filePath, err)
	}
	if len(errorMessage) > 0 {
		return nil, errors.New(errorMessage)
	}

	return storedFiles, nil
}

// storeFile will store the given file to the filestore
// it will make sure that the file is unique by comparing the object's ETag with the file's metadata
func (s *S3BucketSource) storeFile(ctx context.Context, fs *filestore.FileStore, file *RawFileMetadata) (bool, error) {
	logger := log.FromContext(ctx)
	logger.Info("storing file", "file", file.FilePath)

	filePath := file.FilePath
	metadataPath := MetadataPath(filePath)

	// check if the file exists in the filestore

	// for a file to exist in the filestore, both, the file and the metadata file must exist
	fileExists, err := fs.Exists(ctx, filePath)
	if err != nil {
		logger.Error(err, "failed to check if file exists in filestore", "file", filePath)
		return false, err
	}

	metadataExists, err := fs.Exists(ctx, metadataPath)
	if err != nil {
		logger.Error(err, "failed to check if metadata file exists in filestore", "file", metadataPath)
		return false, err
	}

	if fileExists && metadataExists {
		logger.Info("file and metadata file exist in filestore, checking if they are the same", "file",
			filePath, "metadataFile", metadataPath)

		// then compare the metadata file's ETag with the object's ETag
		metadata, err := fs.Retrieve(ctx, metadataPath)
		if err != nil {
			logger.Error(err, "failed to retrieve metadata file from filestore", "file", metadataPath)
			return false, err
		}

		// unmarshal the metadata file into a FileMetadata struct
		var existingFile RawFileMetadata
		err = json.Unmarshal(metadata, &existingFile)
		if err != nil {
			logger.Error(err, "failed to unmarshal metadata file", "file", metadataPath)
			return false, err
		}

		if existingFile.UID == file.UID {
			// the file and the metadata file are the same, so we can skip storing it
			logger.Info("file and metadata file are the same, skipping ...", "file", filePath)
			return false, nil
		}
	}

	// we are here because the file or the metadata file does not exist
	// so we can safely store the file and the corresponding metadata file

	// store the file first — fetch from S3 using the original key
	s3Key := s.s3Key(filePath)
	objectOutput, err := awsclienthandler.GetObject(ctx, s.S3Client, s.Bucket, s3Key)
	if err != nil {
		logger.Error(err, "failed to get object from S3", "file", filePath)
		return false, err
	}

	data, err := io.ReadAll(objectOutput.Body)
	if err != nil {
		logger.Error(err, "failed to read object from S3", "file", filePath)
		return false, err
	}
	if err = fs.Store(ctx, filePath, data); err != nil {
		logger.Error(err, "failed to store file in filestore", "file", filePath)
		return false, err
	}

	metadataData, err := json.Marshal(file)
	if err != nil {
		logger.Error(err, "failed to marshal metadata file", "file", metadataPath)
		return false, err
	}
	if err = fs.Store(ctx, metadataPath, metadataData); err != nil {
		logger.Error(err, "failed to store metadata file in filestore", "file", metadataPath)
		return false, err
	}

	logger.Info("successfully stored file and metadata file in filestore", "file", filePath, "metadataFile", metadataPath)
	return true, nil
}

func (s *S3BucketSource) outputPrefix() string {
	if s.OutputDir != "" {
		return s.OutputDir
	}
	return s.Prefix
}

// filestorePath remaps an S3 key to the filestore output directory.
func (s *S3BucketSource) filestorePath(s3Key string) string {
	if s.OutputDir == "" {
		return s3Key
	}
	baseName := strings.TrimPrefix(s3Key, s.Prefix)
	return path.Join(s.OutputDir, baseName)
}

// s3Key derives the original S3 key from a filestore path.
func (s *S3BucketSource) s3Key(filestorePath string) string {
	if s.OutputDir == "" {
		return filestorePath
	}
	baseName := strings.TrimPrefix(filestorePath, s.OutputDir)
	return path.Join(s.Prefix, baseName)
}
