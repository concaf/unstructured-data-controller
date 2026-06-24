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
	"context"
	"errors"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// StatusPatch snapshots the object, applies mutate, and patches only the status
// diff via merge-patch. No re-fetch or conflict retry needed.
func StatusPatch(ctx context.Context, c client.Client, obj client.Object, mutate func()) error {
	base, ok := obj.DeepCopyObject().(client.Object)
	if !ok {
		return errors.New("DeepCopyObject did not return a client.Object")
	}
	mutate()
	return c.Status().Patch(ctx, obj, client.MergeFrom(base))
}
