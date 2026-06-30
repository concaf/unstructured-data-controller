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
	"testing"
)

func deps(names ...string) []StageDependency {
	d := make([]StageDependency, len(names))
	for i, n := range names {
		d[i] = StageDependency{Name: n}
	}
	return d
}

func crawlerStage(name string, depends ...string) PipelineStage {
	return PipelineStage{
		Name:                name,
		Type:                StageTypeSourceCrawler,
		DependsOn:           deps(depends...),
		SourceCrawlerConfig: &SourceCrawlerConfig{},
	}
}

func docProcStage(name string, depends ...string) PipelineStage {
	return PipelineStage{
		Name:                    name,
		Type:                    StageTypeDocumentProcessor,
		DependsOn:               deps(depends...),
		DocumentProcessorConfig: &DocumentProcessorConfig{},
	}
}

func chunksStage(name string, depends ...string) PipelineStage {
	return PipelineStage{
		Name:                  name,
		Type:                  StageTypeChunksGenerator,
		DependsOn:             deps(depends...),
		ChunksGeneratorConfig: &ChunksGeneratorConfig{},
	}
}

func embedStage(name string, depends ...string) PipelineStage {
	return PipelineStage{
		Name:                            name,
		Type:                            StageTypeVectorEmbeddingsGenerator,
		DependsOn:                       deps(depends...),
		VectorEmbeddingsGeneratorConfig: &VectorEmbeddingsGeneratorConfig{},
	}
}

func destStage(name string, depends ...string) PipelineStage {
	return PipelineStage{
		Name:                    name,
		Type:                    StageTypeDestinationSyncer,
		DependsOn:               deps(depends...),
		DestinationSyncerConfig: &DestinationSyncerConfig{},
	}
}

func TestValidateStages_ValidLinearChain(t *testing.T) {
	stages := []PipelineStage{
		crawlerStage("crawl"),
		docProcStage("convert", "crawl"),
		chunksStage("chunk", "convert"),
	}
	if err := ValidateStages(stages); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestValidateStages_ValidFanOut(t *testing.T) {
	stages := []PipelineStage{
		crawlerStage("crawl"),
		docProcStage("convert-a", "crawl"),
		docProcStage("convert-b", "crawl"),
	}
	if err := ValidateStages(stages); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestValidateStages_ValidFanIn(t *testing.T) {
	stages := []PipelineStage{
		crawlerStage("crawl-a"),
		crawlerStage("crawl-b"),
		docProcStage("convert", "crawl-a", "crawl-b"),
	}
	if err := ValidateStages(stages); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestValidateStages_ValidDiamond(t *testing.T) {
	stages := []PipelineStage{
		crawlerStage("crawl"),
		docProcStage("convert-a", "crawl"),
		docProcStage("convert-b", "crawl"),
		chunksStage("chunk", "convert-a", "convert-b"),
	}
	if err := ValidateStages(stages); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestValidateStages_MissingDependency(t *testing.T) {
	stages := []PipelineStage{
		docProcStage("convert", "crawl"),
	}
	if err := ValidateStages(stages); err == nil {
		t.Error("expected error for missing dependency")
	}
}

func TestValidateStages_CycleDetection(t *testing.T) {
	stages := []PipelineStage{
		crawlerStage("a", "c"),
		docProcStage("b", "a"),
		chunksStage("c", "b"),
	}
	if err := ValidateStages(stages); err == nil {
		t.Error("expected error for cycle")
	}
}

func TestValidateStages_SelfCycle(t *testing.T) {
	stages := []PipelineStage{
		crawlerStage("a", "a"),
	}
	if err := ValidateStages(stages); err == nil {
		t.Error("expected error for self-cycle")
	}
}

func TestConditionTypeForStage(t *testing.T) {
	tests := []struct {
		stageType StageType
		want      string
	}{
		{StageTypeSourceCrawler, SourceCrawlerCondition},
		{StageTypeDocumentProcessor, DocumentProcessorCondition},
		{StageTypeChunksGenerator, ChunksGeneratorCondition},
		{StageTypeVectorEmbeddingsGenerator, VectorEmbeddingGenerationConditionType},
		{StageTypeDestinationSyncer, DestinationSyncerCondition},
		{StageType("Unknown"), ""},
	}
	for _, tt := range tests {
		got := ConditionTypeForStage(tt.stageType)
		if got != tt.want {
			t.Errorf("ConditionTypeForStage(%s) = %q, want %q", tt.stageType, got, tt.want)
		}
	}
}

func TestValidateStages_FullPipeline(t *testing.T) {
	stages := []PipelineStage{
		crawlerStage("crawl"),
		docProcStage("convert", "crawl"),
		chunksStage("chunk", "convert"),
		embedStage("embed", "chunk"),
		destStage("sync", "embed"),
	}
	if err := ValidateStages(stages); err != nil {
		t.Errorf("expected no error for full pipeline, got: %v", err)
	}
}
