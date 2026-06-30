# Unstructured Data Controller with LocalStack

Simple guide to get the Unstructured Data Controller up and running using [LocalStack](https://localstack.cloud/) for local development and testing.

## Prerequisites

- [Docker](https://docs.docker.com/get-docker/) (or Podman with Docker socket)
- Kubernetes cluster (or Kind for local dev)
- [AWS CLI](https://aws.amazon.com/cli/) with [awslocal](https://github.com/localstack/awscli-local)
- [Docling-server](https://github.com/docling-project/docling-serve) using `pip install "docling-serve[ui]"`
- [Ollama](https://ollama.com/) for embedding models
- `kubectl` and access to a Kubernetes cluster where the unstructured-data-controller is deployed
- **LocalStack setup:** Follow [Setup LocalStack](setup-localstack.md) first if you haven't already.

### 1. Create Namespace

```bash
kubectl create namespace unstructured-controller-namespace
```

## 2. Run docling locally

```bash
docling-serve run --enable-ui
```

## 3. Run Ollama locally

```bash
ollama serve
ollama pull nomic-embed-text:latest
ollama cp nomic-embed-text:latest nomic-ai/nomic-embed-text-v1.5
```

### 4. Create secrets

The controller and pipeline stages each reference their own secrets. Create them:

```bash
kubectl apply -f config/samples/unstructured-secret.yaml -n unstructured-controller-namespace
```

This creates:
- `operator-secret` — filestore S3 credentials + docling key (used by ControllerConfig)
- `source-aws-creds` — source S3 credentials (used by SourceCrawler)
- `dest-aws-creds` — destination S3 credentials (used by DestinationSyncer)
- `embedding-api-creds` — embedding model endpoint + API key (used by VectorEmbeddingsGenerator)

### 5. Setup local cache directory

```bash
mkdir -p tmp/cache/
```

### 6. Deploy Controller

```bash
make install  # install CRDs
make run      # run controller locally
```

## 7. Create the ControllerConfig

The ControllerConfig sets up operator-level infrastructure (filestore, docling, concurrency limits):

```bash
kubectl apply -f config/samples/operator_v1alpha1_controllerconfig.yaml -n unstructured-controller-namespace
```

Verify it's healthy:

```bash
kubectl get controllerconfig controllerconfig -o yaml
```

The status should show `ConfigReady` with `status: "True"`.

## 8. Create the UnstructuredDataPipeline

```bash
kubectl apply -f config/samples/operator_v1alpha1_unstructureddatapipeline.yaml -n unstructured-controller-namespace
```

This creates the pipeline and all stage CRs automatically. Each stage watches its upstream dependencies and processes files as they appear.

## Verifying

Check pipeline and stage statuses:

```bash
kubectl get unstructureddatapipeline -o wide
kubectl get sourcecrawler -o wide
kubectl get documentprocessor -o wide
kubectl get chunksgenerator -o wide
kubectl get vectorembeddingsgenerator -o wide
kubectl get destinationsyncer -o wide
```
