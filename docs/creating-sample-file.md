# Unstructured Data Controller - Quick Start Guide

Simple guide to get the first file processed by unstructured controller

## What It Does

The controller automatically processes unstructured files from S3:
1. **Reads files** from S3 bucket
2. **Converts** them to Markdown using Docling
3. **Chunks** the content for better processing
4. **Stores** the results in S3

---

## Prerequisites

- Have local controller running using [Unstructured Data Controller with LocalStack](setup-unstructured-controller.md)

---

## Quick Setup

### 1. Create S3 Buckets

Create the ingestion and output buckets using the AWS CLI (or awslocal for LocalStack):

```bash
awslocal s3 mb s3://data-ingestion-bucket
awslocal s3 mb s3://output-chunks-bucket
awslocal s3 mb s3://data-storage-bucket
```

### 2. Create Unstructured Data Pipeline

**Apply UnstructuredDataPipeline:**
```bash
kubectl apply -f config/samples/operator_v1alpha1_unstructureddatapipeline.yaml -n unstructured-controller-namespace
```

---

## Test It

### Upload a File

```bash
# Upload to S3
aws s3 cp test.pdf s3://data-ingestion-bucket/testunstructureddataproduct/

# The controller will automatically:
# 1. Download the file
# 2. Convert it to Markdown
# 3. Chunk the content
# 4. Upload to S3 destination
```

### Check Results in S3

```bash
awslocal s3 ls s3://output-chunks-bucket/testunstructureddataproduct/
```

### Monitor Progress

```bash
# Check UnstructuredDataPipeline status
kubectl get unstructureddatapipeline -n unstructured-controller-namespace

# Check DocumentProcessor status
kubectl get documentprocessor -n unstructured-controller-namespace

# Check ChunksGenerator status
kubectl get chunksgenerator -n unstructured-controller-namespace

# View controller logs
kubectl logs -f deployment/unstructured-data-controller -n unstructured-controller-namespace
```

---

## Configuration

The `UnstructuredDataPipeline` CR defines the complete pipeline:

```yaml
apiVersion: operator.dataverse.redhat.com/v1alpha1
kind: UnstructuredDataPipeline
metadata:
  name: testunstructureddataproduct
spec:
  # Where to read files from
  sourceConfig:
    type: s3
    s3Config:
      bucket: data-ingestion-bucket
      prefix: testunstructureddataproduct

  # How to convert files
  documentProcessorConfig:
    type: docling
    doclingConfig:
      from_formats: [pdf, docx, md]
      do_ocr: true

  # How to chunk content
  chunksGeneratorConfig:
    strategy: markdownTextSplitter
    markdownSplitterConfig:
      chunkSize: 1000
      chunkOverlap: 200

  # Where to store results
  destinationConfig:
    type: s3
    s3DestinationConfig:
      bucket: output-chunks-bucket
      prefix: testunstructureddataproduct
```

---

## Expected Output

After processing, each file produces:

1. **Local Cache** (`tmp/cache/testunstructureddataproduct/`):
   - `file.pdf` - Original file
   - `file.pdf-metadata.json` - File metadata
   - `file.pdf-converted.json` - Converted Markdown
   - `file.pdf-chunks.json` - Chunked content

2. **S3 Destination** (`s3://output-chunks-bucket/testunstructureddataproduct/`):
   - `file.pdf-chunks.json` - Complete processed file with:
     - `convertedDocument`: Original conversion with metadata
     - `chunksDocument`: Chunked text ready for processing

