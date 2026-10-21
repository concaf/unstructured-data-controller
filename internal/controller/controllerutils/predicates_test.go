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
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const testConditionType = "TestReady"

func TestIsAlreadyReconciled(t *testing.T) {
	tests := []struct {
		name                      string
		generation                int64
		lastAppliedGeneration     int64
		conditions                []metav1.Condition
		conditionType             string
		expectedAlreadyReconciled bool
	}{
		{
			name:                  "generation matches and condition is true",
			generation:            2,
			lastAppliedGeneration: 2,
			conditions: []metav1.Condition{
				{Type: testConditionType, Status: metav1.ConditionTrue},
			},
			conditionType:             testConditionType,
			expectedAlreadyReconciled: true,
		},
		{
			name:                  "generation does not match",
			generation:            3,
			lastAppliedGeneration: 2,
			conditions: []metav1.Condition{
				{Type: testConditionType, Status: metav1.ConditionTrue},
			},
			conditionType:             testConditionType,
			expectedAlreadyReconciled: false,
		},
		{
			name:                  "generation matches but condition is false",
			generation:            2,
			lastAppliedGeneration: 2,
			conditions: []metav1.Condition{
				{Type: testConditionType, Status: metav1.ConditionFalse},
			},
			conditionType:             testConditionType,
			expectedAlreadyReconciled: false,
		},
		{
			name:                  "generation matches but condition is unknown",
			generation:            2,
			lastAppliedGeneration: 2,
			conditions: []metav1.Condition{
				{Type: testConditionType, Status: metav1.ConditionUnknown},
			},
			conditionType:             testConditionType,
			expectedAlreadyReconciled: false,
		},
		{
			name:                      "generation matches but no conditions",
			generation:                1,
			lastAppliedGeneration:     1,
			conditions:                nil,
			conditionType:             testConditionType,
			expectedAlreadyReconciled: false,
		},
		{
			name:                  "generation matches but wrong condition type",
			generation:            1,
			lastAppliedGeneration: 1,
			conditions: []metav1.Condition{
				{Type: "OtherCondition", Status: metav1.ConditionTrue},
			},
			conditionType:             testConditionType,
			expectedAlreadyReconciled: false,
		},
		{
			name:                      "brand new object with zero generation",
			generation:                1,
			lastAppliedGeneration:     0,
			conditions:                nil,
			conditionType:             testConditionType,
			expectedAlreadyReconciled: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsAlreadyReconciled(tt.generation, tt.lastAppliedGeneration, tt.conditions, tt.conditionType)
			if result != tt.expectedAlreadyReconciled {
				t.Errorf("IsAlreadyReconciled() = %v, want %v", result, tt.expectedAlreadyReconciled)
			}
		})
	}
}
