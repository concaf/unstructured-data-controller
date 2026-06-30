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

package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/redhat-data-and-ai/unstructured-data-controller/pkg/auth"
	"github.com/redhat-data-and-ai/unstructured-data-controller/pkg/k8sclient"
	"github.com/redhat-data-and-ai/unstructured-data-controller/pkg/snowflake"
)

// CombinedResult contains both Kubernetes pipelines and Snowflake databases
type CombinedResult struct {
	Pipelines []k8sclient.PipelineInfo `json:"pipelines"`
	Databases []snowflake.DatabaseInfo `json:"databases"`
}

// RegisterListPipelines registers the list_unstructured_data_pipelines_for_user MCP tool
func RegisterListPipelines(s *mcp.Server, k8sClient *k8sclient.Client) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_unstructured_data_pipelines_for_user",
		Description: "List all UnstructuredDataPipeline custom resources and Snowflake databases the authenticated user has access to",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		// Extract OAuth token from context
		oauthToken, ok := auth.AccessTokenFromContext(ctx)
		if !ok {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{
					Text: "Error: OAuth token not found in context",
				}},
				IsError: true,
			}, nil, nil
		}

		// List Kubernetes UnstructuredDataPipeline CRs
		var pipelines []k8sclient.PipelineInfo
		if k8sClient != nil {
			var err error
			pipelines, err = k8sClient.ListPipelines(ctx)
			if err != nil {
				return &mcp.CallToolResult{
					Content: []mcp.Content{&mcp.TextContent{
						Text: fmt.Sprintf("Error listing pipelines: %v", err),
					}},
					IsError: true,
				}, nil, nil
			}
		}

		// Query Snowflake databases using the user's OAuth token
		databases, err := snowflake.ShowDatabases(ctx, oauthToken)
		if err != nil {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{
					Text: fmt.Sprintf("Error querying Snowflake: %v", err),
				}},
				IsError: true,
			}, nil, nil
		}

		// Combine results
		result := CombinedResult{
			Pipelines: pipelines,
			Databases: databases,
		}

		jsonBytes, err := json.Marshal(result)
		if err != nil {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{
					Text: fmt.Sprintf("Error marshaling result: %v", err),
				}},
				IsError: true,
			}, nil, nil
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{
				Text: fmt.Sprintf("Found %d pipeline(s) and %d database(s):\n%s",
					len(pipelines), len(databases), string(jsonBytes)),
			}},
		}, nil, nil
	})
}
