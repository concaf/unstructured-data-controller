---
globs: internal/controller/**/*.go
---

# Controller Conventions

Best practices from controller-runtime, cluster-api, and cert-manager take precedence over existing code in this repo. If existing code contradicts Go or Kubernetes controller best practices, do not copy it - challenge it and write it correctly.

## Reconciler Structure
- Every reconciler struct embeds `client.Client` and has `Scheme *runtime.Scheme`
- `SetupWithManager` uses `GenerationChangedPredicate{}` on the primary resource and watches dependent resources with appropriate predicates
- RBAC markers use namespace-scoped format: `+kubebuilder:rbac:groups=operator.dataverse.redhat.com,namespace=unstructured-controller-namespace,resources=...`
- Break reconciliation into focused helper methods - never dump all logic into `Reconcile()`

## Reconcile Flow
1. `log.FromContext(ctx)` - never create standalone loggers
2. Check `IsConfigCRHealthy()` - requeue if not ready
3. `Get` the CR - return nil on NotFound (resource deleted, not an error)
4. Check `IsAlreadyReconciled()` - skip expensive work on restart (see Restart Behavior below)
5. Reconcile logic in focused helper methods
6. On error: update CR status with the error, return the error. Do NOT log the error AND return it - that double-logs.

## Restart Behavior

When a pod restarts or a new version is deployed, the informer does a full List of every CR and fires Create events for all of them. Any pending `RequeueAfter` items in the work queue are lost. Understanding this is critical for writing correct controllers.

### What happens on restart
1. The work queue is empty (all RequeueAfter timers are gone)
2. The informer re-lists every existing CR as a Create event
3. controller-runtime's priority queue assigns priority -100 to these initial-list items
4. Any new CR created by a user after startup gets priority 0 (processed first)
5. With concurrency > 1, multiple workers process the backlog in parallel

### The IsAlreadyReconciled pattern
Check `controllerutils.IsAlreadyReconciled()` inside `Reconcile()` after fetching the CR. If the spec hasn't changed (`LastAppliedGeneration == Generation` and condition is healthy), skip expensive work but still return `RequeueAfter` to restart polling loops:

```go
if controllerutils.IsAlreadyReconciled(cr.Generation, cr.Status.LastAppliedGeneration, cr.Status.Conditions, ConditionType) {
    logger.V(1).Info("spec unchanged, restarting poll timer")
    return ctrl.Result{RequeueAfter: resyncInterval}, nil
}
```

For controllers that don't poll (e.g., UnstructuredDataPipeline), return `ctrl.Result{}` instead.

This is the pattern used by Crossplane and cert-manager: let all CRs reconcile on restart, but make the reconcile cheap. It avoids thundering herd on external services while keeping the polling loop alive.

### Do NOT use predicates for restart optimization
Never use a predicate to filter out Create events based on reconciliation state. Predicates block the Create event entirely, which means:
- The CR never enters the work queue
- No `RequeueAfter` is ever returned
- The periodic polling loop (S3 crawling, task status checking) never restarts
- The controller is effectively dead for that CR until its spec changes

`GenerationChangedPredicate` is safe because it only filters Update events (its `Create()` returns true). Custom predicates that filter Create events based on status fields are not safe.

### Priority queue behavior
controller-runtime's priority queue automatically handles restart prioritization. Do NOT manually set `result.Priority` to lower the priority of requeued items - it's unnecessary because:
- Restart re-list items are dequeued at priority -100 (set by the framework)
- When a reconcile returns `RequeueAfter` without setting `result.Priority`, controller-runtime **preserves the original dequeue priority**
- So restart items stay at -100 on requeue, and new CRs at priority 0 always jump ahead
- Setting an explicit lower priority (e.g., -200) adds no value since -100 is already below 0

### Concurrency
Per-controller concurrency is configured via environment variables (e.g., `CONCURRENCY_DOCUMENT_PROCESSOR=10`) with a default of 5 workers. This is set at startup via `config.Controller.GroupKindConcurrency` in the manager options. The work queue guarantees the same CR is never reconciled by two workers simultaneously, so increasing concurrency is safe for idempotent reconcilers.

## Error Handling
- Always check `if err != nil` - never use `if err == nil` as the happy-path guard
- Handle errors once: update status OR log, then return. Never log AND return the same error.
- Status updates: always use `controllerutils.StatusPatch` - re-fetches object before mutating
- Never return both error AND `Requeue: true` - error already implies requeue with backoff
- Wrap errors with context: `fmt.Errorf("creating deployment for %s: %w", name, err)`
- The existing `handleError` pattern double-logs (logs + returns). New code should not copy this - update status and return, let the caller log.

## Logging
- Use structured key-value pairs: `logger.Info("Reconciling", "controller", ControllerName)`
- Log at reconcile entry and meaningful state transitions
- `log.Error(err, ...)` for real errors. `log.Info(...)` for expected conditions like NotFound
- Import alias `operatorv1alpha1` for API types package
