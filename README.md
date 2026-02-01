# Chronos Kubernetes Scheduler

[![Tests](https://github.com/rohitraut3366/chronos-kubernetes-scheduler/workflows/Tests/badge.svg)](https://github.com/rohitraut3366/chronos-kubernetes-scheduler/actions)
[![Coverage](https://img.shields.io/badge/coverage-96%25-brightgreen.svg)](https://github.com/rohitraut3366/chronos-kubernetes-scheduler/actions)
[![Go Report Card](https://goreportcard.com/badge/github.com/rohitraut3366/chronos-kubernetes-scheduler)](https://goreportcard.com/report/github.com/rohitraut3366/chronos-kubernetes-scheduler)

Kubernetes custom scheduler plugin that intelligently bin-packs workloads based on job durations, maximizing cluster resource utilization and consolidation for workloads with predictable durations.

## 🎯 Overview

The **Chronos** scheduler plugin uses job duration annotations to intelligently bin-pack workloads, prioritizing consolidation over spreading. This approach:

- **Bin-Packing First**: Fits new jobs into existing time windows when possible
- **Maximizes Consolidation**: Prefers nodes with longer remaining work for better packing  
- **Minimizes Extensions**: When jobs must extend beyond existing work, chooses the node requiring minimal extension
- **Avoids Empty Nodes**: Heavily penalizes empty nodes to enable cluster cost optimization

## 🏗️ Architecture

```
chronos-kubernetes-scheduler/
├── cmd/
│   └── scheduler/
│       └── main.go                    # Application entry point
├── internal/
│   └── scheduler/
│       ├── plugin.go                  # Core scheduler logic
│       ├── plugin_test.go             # Unit tests (500+ test cases)
│       └── plugin_integration_test.go # Integration tests
├── charts/
│   └── chronos-kubernetes-scheduler/  # Helm chart for deployment
│       ├── Chart.yaml
│       ├── values.yaml
│       └── templates/                 # Kubernetes manifests
├── build/
│   └── Dockerfile                     # Multi-stage container build
├── examples/                          # Example workload manifests
├── bin/                               # Built binaries
├── Makefile                           # Build automation
└── go.mod                             # Go module definition
```

## 🚀 Quick Start

### Prerequisites
- Kubernetes 1.28+
- Helm 3.8+ (for installation)

### Development Prerequisites (Optional)
Only needed for building from source:
- Go 1.25+
- Docker (for building images)

```bash
# Essential commands
make test          # All tests
make build         # Build binary
make docker-build  # Build Docker image

# Additional targets
make help          # Show all available targets
make test-unit     # Unit tests only  
make test-integration  # Integration tests only
make bench         # Performance benchmarks
make clean         # Clean build artifacts
make fmt           # Format code
make lint          # Run linter
make coverage      # Generate coverage report
```



## 📋 Usage

### 1. Annotate Your Pods

Add the duration annotation to pods that should use this scheduler:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: my-batch-job
  annotations:
    scheduling.workload.io/expected-duration-seconds: "300.5"  # Supports decimal values (5 min 30 sec)
spec:
  schedulerName: chronos-kubernetes-scheduler
  containers:
  - name: worker
    image: my-batch-job:latest
```

### 2. Deploy the Scheduler

```bash
# Install
helm install chronos-scheduler ./charts/chronos-kubernetes-scheduler

# Install with custom namespace
helm install chronos-scheduler ./charts/chronos-kubernetes-scheduler -n scheduler-system --create-namespace
```

### 3. Verify Installation

```bash
# Check scheduler pods
kubectl get pods -l app.kubernetes.io/name=chronos-kubernetes-scheduler

# View scheduler logs
kubectl logs -l app.kubernetes.io/name=chronos-kubernetes-scheduler --tail=100

# Test with example workloads
kubectl apply -f examples/
```

## 🔧 Configuration

### Scheduler Configuration

The scheduler uses a ConfigMap for configuration:

```yaml
apiVersion: kubescheduler.config.k8s.io/v1
kind: KubeSchedulerConfiguration
profiles:
- schedulerName: chronos-kubernetes-scheduler
  plugins:
    score:
      enabled:
      - name: Chronos
      - name: NodeResourcesFit  # Default resource-based tie-breaker
```


## 📊 Live Analysis with K9s Plugin

Real-time scheduling analysis directly in K9s. See [k9s/README.md](k9s/README.md) for setup.

## 🙏 Acknowledgments

- Built using the [Kubernetes Scheduler Framework](https://kubernetes.io/docs/concepts/scheduling-eviction/scheduling-framework/)
- Inspired by production workload optimization needs
