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

package googledrive

import (
	"context"
	"fmt"

	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	mimeTypeFolder   = "application/vnd.google-apps.folder"
	mimeTypeShortcut = "application/vnd.google-apps.shortcut"
)

func NewDriveService(ctx context.Context, serviceAccountJSON []byte) (*drive.Service, error) {
	// TODO: move this to a non deprecated method
	creds, err := google.CredentialsFromJSON(ctx, serviceAccountJSON, drive.DriveReadonlyScope)
	if err != nil {
		return nil, err
	}
	return drive.NewService(ctx, option.WithCredentials(creds))
}

// CollectAllFolderIDs recursively collects all descendant folder IDs starting from the given
// root folder IDs. It follows shortcuts that point to folders and includes those target folders
// and their descendants as well. The returned set includes the root folder IDs themselves.
func CollectAllFolderIDs(ctx context.Context, srv *drive.Service, rootFolderIDs []string) (map[string]bool, error) {
	logger := log.FromContext(ctx)
	allFolderIDs := map[string]bool{}
	visited := map[string]bool{}

	var walk func(folderID string) error
	walk = func(folderID string) error {
		if visited[folderID] {
			return nil
		}
		visited[folderID] = true
		allFolderIDs[folderID] = true

		// list all children that are folders or shortcuts
		query := fmt.Sprintf("'%s' in parents and trashed = false and (mimeType = '%s' or mimeType = '%s')",
			folderID, mimeTypeFolder, mimeTypeShortcut)

		err := srv.Files.List().
			Q(query).
			Fields("nextPageToken", "files(id,mimeType,shortcutDetails)").
			Context(ctx).
			Pages(ctx, func(fl *drive.FileList) error {
				for _, f := range fl.Files {
					switch f.MimeType {
					case mimeTypeFolder:
						logger.Info("found subfolder", "folderID", f.Id, "parentID", folderID)
						if err := walk(f.Id); err != nil {
							return err
						}
					case mimeTypeShortcut:
						if f.ShortcutDetails == nil {
							continue
						}
						// only follow shortcuts that point to folders
						if f.ShortcutDetails.TargetMimeType == mimeTypeFolder {
							logger.Info("found shortcut to folder", "shortcutID", f.Id, "targetFolderID", f.ShortcutDetails.TargetId)
							if err := walk(f.ShortcutDetails.TargetId); err != nil {
								return err
							}
						}
					}
				}
				return nil
			})
		return err
	}

	for _, rootID := range rootFolderIDs {
		if err := walk(rootID); err != nil {
			return nil, fmt.Errorf("failed to collect folder IDs from root %s: %w", rootID, err)
		}
	}

	logger.Info("collected all folder IDs", "count", len(allFolderIDs))
	return allFolderIDs, nil
}
