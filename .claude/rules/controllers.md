---
globs: internal/controller/**/*.go
---

# Controller Conventions

Follow the pattern in `internal/controller/sourcecrawler_controller.go`.

## Reconciler Structure
- Every reconciler struct embeds `client.Client` and has `Scheme *runtime.Scheme`
- `SetupWithManager` uses `GenerationChangedPredicate{}` on the primary resource and watches dependent resources with appropriate predicates
- RBAC markers use namespace-scoped format: `+kubebuilder:rbac:groups=operator.dataverse.redhat.com,namespace=unstructured-controller-namespace,resources=...`

## Reconcile Flow
1. `log.FromContext(ctx)` — never create standalone loggers
2. Check `IsConfigCRHealthy()` — requeue if not ready
3. `Get` the CR — return nil on NotFound (resource deleted, not an error)
4. `SetWaiting()` — reset status before reconciliation
5. Reconcile logic — break into focused helper methods, do not dump everything in Reconcile()
6. On error: use `handleError` pattern (update CR status with error, return error)

## Error Handling
- Use the `handleError` method: log error, update CR status, return error
- Always check `if err != nil` — never use `if err == nil` as the happy-path guard
- Status updates: always use `controllerutils.StatusPatch` — re-fetches object before mutating
- Never return both error AND `Requeue: true` — error already implies requeue with backoff
- Wrap errors with context: `fmt.Errorf("creating deployment for %s: %w", name, err)`

## Logging
- Use structured key-value pairs: `logger.Info("Reconciling", "controller", ControllerName)`
- Log at reconcile entry and meaningful state transitions
- `log.Error(err, ...)` for real errors. `log.Info(...)` for expected conditions like NotFound
- Import alias `operatorv1alpha1` for API types package
