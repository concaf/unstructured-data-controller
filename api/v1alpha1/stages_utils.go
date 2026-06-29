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

package v1alpha1

import (
	"fmt"
	"strings"
)

// ValidateStages checks that all dependency references exist and the DAG has no cycles.
func ValidateStages(stages []PipelineStage) error {
	stageMap := make(map[string]bool, len(stages))
	for _, stage := range stages {
		stageMap[stage.Name] = true
	}

	for _, stage := range stages {
		for _, dep := range stage.DependsOn {
			if !stageMap[dep.Name] {
				return fmt.Errorf("stage %q depends on undefined stage %q", stage.Name, dep.Name)
			}
		}
	}

	return detectCycles(stages)
}

// detectCycles checks for circular dependencies in the pipeline stages.
func detectCycles(stages []PipelineStage) error {
	// for each stage, count how many stages it depends on
	pendingDeps := map[string]int{}
	// for each stage, track which downstream stages depend on it
	downstreamStages := map[string][]string{}
	for _, stage := range stages {
		pendingDeps[stage.Name] = len(stage.DependsOn)
		for _, dep := range stage.DependsOn {
			downstreamStages[dep.Name] = append(downstreamStages[dep.Name], stage.Name)
		}
	}

	// stages with no dependencies are ready to process
	var readyStages []string
	for _, stage := range stages {
		if pendingDeps[stage.Name] == 0 {
			readyStages = append(readyStages, stage.Name)
		}
	}

	// process ready stages: for each one, decrement the pending count
	// of its downstream stages. if any reach zero, they become ready too.
	processed := 0
	for len(readyStages) > 0 {
		stage := readyStages[0]
		readyStages = readyStages[1:]
		processed++
		for _, downstream := range downstreamStages[stage] {
			pendingDeps[downstream]--
			if pendingDeps[downstream] == 0 {
				readyStages = append(readyStages, downstream)
			}
		}
	}

	// stages that never became ready are stuck in a circular dependency
	if processed != len(stages) {
		var stuck []string
		for name, remaining := range pendingDeps {
			if remaining > 0 {
				stuck = append(stuck, name)
			}
		}
		return fmt.Errorf("cycle detected involving stages: %s", strings.Join(stuck, ", "))
	}
	return nil
}

// ConditionTypeForStage maps a StageType to its corresponding CR condition string.
func ConditionTypeForStage(t StageType) string {
	switch t {
	case StageTypeSourceCrawler:
		return SourceCrawlerCondition
	case StageTypeDocumentProcessor:
		return DocumentProcessorCondition
	case StageTypeChunksGenerator:
		return ChunksGeneratorCondition
	case StageTypeVectorEmbeddingsGenerator:
		return VectorEmbeddingGenerationConditionType
	case StageTypeDestinationSyncer:
		return DestinationSyncerCondition
	default:
		return ""
	}
}
