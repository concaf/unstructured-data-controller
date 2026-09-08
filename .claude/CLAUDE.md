# Unstructured Data Controller

Kubernetes operator (kubebuilder) for unstructured data pipelines — crawling sources, processing documents, chunking, generating embeddings, and syncing to destinations.

## Commands

- `make lint` — golangci-lint + yamllint. Run before every commit.
- `make test` — unit tests with envtest.
- `make build` — build the manager binary.
- `make manifests generate` — regenerate CRDs and DeepCopy after any `api/v1alpha1/` changes.
- `go mod tidy && go mod vendor` — after any dependency changes.

## Architecture

- **API types**: `api/v1alpha1/` — CRDs: ControllerConfig, UnstructuredDataPipeline, SourceCrawler, DocumentProcessor, ChunksGenerator, VectorEmbeddingsGenerator, DestinationSyncer
- **Controllers**: `internal/controller/` — one reconciler per CRD
- **Controller utils**: `internal/controller/controllerutils/` — shared status, predicate, and patch helpers
- **MCP server**: `internal/mcp/`, `cmd/unstructured-data-mcp-server/`
- **Packages**: `pkg/` — auth, awsclienthandler, cache, docling, embedding, filestore, gdrive, k8sclient, langchain, logger, snowflake, unstructured
- **Generated files**: `zz_generated.deepcopy.go` — never edit, run `make manifests generate`
- **Vendor**: committed. Never modify by hand.

## Go Style

Write idiomatic Go. Follow Effective Go, Go Code Review Comments, and the Google Go Style Guide. Study the Go standard library (`net/http`, `io`, `context`) for patterns.

### Naming

- Descriptive names always. No single-letter variables except `i`/`j` in loops, method receivers, and `ctx`/`err` in tiny scopes.
- Name K8s objects by their kind: `crawlerDeployment`, `processorPod`, not `dep` or `p`.
- Acronyms are all-caps: `ID`, `HTTP`, `URL`, `API` — never `Id`, `Http`.
- No getters: `Name()` not `GetName()`. The package name provides context.
- Import aliases follow convention: `operatorv1alpha1`, `ctrl`, `appsv1`, `corev1`.

### Error Handling

- Never ignore errors. Every `error` return must be checked — no `_, _ = foo()`.
- Wrap with context: `fmt.Errorf("creating deployment for crawler %s: %w", crawler.Name, err)`.
- Handle errors once: either log OR return, never both. Double-logging makes debugging harder.
- Use `errors.Is`/`errors.As` for matching, never string comparison or `==`.
- Never return both an error AND `Requeue: true` — error implies requeue with backoff.

### Logging

- Always `log.FromContext(ctx)` — never global loggers or `fmt.Println`.
- Structured key-value pairs: `log.Info("Created Deployment", "deployment", name, "namespace", ns)` — never `fmt.Sprintf` in messages.
- Log enough to debug from logs alone: reconcile entry, state transitions, decisions, handled non-errors.
- `log.Error(err, ...)` for real errors. `log.Info(...)` for expected conditions (NotFound, already exists).
- `log.V(1).Info(...)` for verbose/debug detail only.

## Controller Patterns

Reference these well-written operators for patterns: kubernetes-sigs/cluster-api (condition management, patch helpers), cert-manager/cert-manager (error handling, resilience), kubernetes-sigs/kueue (modern controller-runtime usage), fluxcd/source-controller (clean reconciler structure).

### Reconciler Structure

- Level-triggered, not edge-triggered — reconcile from observed state, never branch on event type.
- Idempotent — running twice with same inputs produces same result.
- See `internal/controller/sourcecrawler_controller.go` for the established pattern in this project:
  - `log.FromContext(ctx)` → `IsConfigCRHealthy()` → `Get` CR → `SetWaiting()` → reconcile logic → `handleError` on failure
  - `handleError` logs the error AND updates CR status, then returns the error
  - `SetupWithManager` uses `GenerationChangedPredicate{}` and watches dependent resources
  - Status updates via `controllerutils.StatusPatch` — re-fetch before mutating

### Anti-Patterns to Avoid

- Dumping all logic into `Reconcile()` — break into focused helper methods
- Silently swallowing errors with `_ =`
- Logging an error and also returning it (double-logging)
- Overly broad RBAC — use minimum required verbs/resources
- Making external API calls on every reconcile without checking generation/state
