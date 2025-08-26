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
# Run all tests
make test

# Build binary  
make build

# Build Docker image
make docker-build
```

### Deploy to Kubernetes

```bash
# Deploy using Helm
helm install chronos-scheduler ./charts/chronos-kubernetes-scheduler

# Check deployment status  
kubectl get pods -l app.kubernetes.io/name=chronos-kubernetes-scheduler

# View logs
kubectl logs -l app.kubernetes.io/name=chronos-kubernetes-scheduler

# Remove deployment
helm uninstall chronos-scheduler
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

#### Option A: Quick Helm Install
```bash
# Install directly from local chart
helm install chronos-scheduler ./charts/chronos-kubernetes-scheduler

# Install with custom namespace
helm install chronos-scheduler ./charts/chronos-kubernetes-scheduler -n scheduler-system --create-namespace
```

#### Option B: Custom Configuration
```bash
# Create custom values file
cat > my-values.yaml <<EOF
replicaCount: 3  # High availability

scheduler:
  leaderElection:
    enabled: true

resources:
  requests:
    cpu: 200m
    memory: 256Mi
  limits:
    cpu: 1000m
    memory: 1Gi
EOF

# Install with custom values
helm install chronos-scheduler ./charts/chronos-kubernetes-scheduler -f my-values.yaml
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

### 4. Scheduler Selection Logic

The plugin uses a hierarchical scoring algorithm with three priorities:

**Priority 1 (Highest): Bin-Packing** - Job fits within existing time windows
```
baseScore = 1,000,000 + (maxRemainingTime * 100)  // Consolidation bonus
```

**Priority 2 (Medium): Extension Minimization** - Job extends beyond existing work
```
baseScore = 100,000 - (extensionTime * 100)  // Penalty for extending
```

**Priority 3 (Lowest): Empty Nodes** - Heavily penalized for cost optimization
```
baseScore = 1,000  // Low score to avoid empty nodes
```

**Example Scoring:**
- **Node with 600s remaining work, new 300s job**: `1,000,000 + (600 * 100) = 1,060,000` ✅ (Bin-packing)
- **Node with 200s remaining work, new 300s job**: `100,000 - (100 * 100) = 90,000` (Extension)
- **Empty Node**: `1,000` (Heavily penalized)
- **Result**: Consolidation wins for optimal bin-packing! ✅

## 🧪 Testing

### Comprehensive Test Suite

- **✅ 500+ Unit Tests**: Algorithm logic, edge cases, error handling
- **✅ Integration Tests**: Real Kubernetes object interactions  
- **✅ Performance Tests**: Nanosecond-level scoring benchmarks
- **✅ Property Tests**: Randomized correctness validation
- **✅ Realistic Scenarios**: Production cluster simulations

### Run Specific Tests

```bash
# Unit tests only
make test-unit

# Integration tests only  
make test-integration

# Performance benchmarks
make bench

# Test coverage report
make coverage
```

### Example Test Output

```
🔗 Integration tests with realistic Kubernetes objects
🎯 Realistic production web application cluster
  📊 web-frontend: score=84 (bin-packing case)
  📊 api-backend: score=75 (extension case)  
  📊 database: score=91 (best consolidation)
  📊 cache-redis: score=10 (empty node penalty)
🏆 Winner: database with score 91 (optimal consolidation)
✅ Integration test passed!
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

### Integration Testing

```bash
# Run full integration tests with K3s
make integration-setup

# Quick integration test in existing cluster
make integration-quick
```

## 📊 Live Analysis with K9s Plugin

Real-time scheduling analysis directly in K9s. See [k9s/README.md](k9s/README.md) for setup.

## 🛠️ Development

### Project Structure Benefits

- **📦 Clean Separation**: `cmd/` for binaries, `internal/` for logic
- **🧪 Comprehensive Testing**: Separate unit and integration test files  
- **🚀 Easy Builds**: Makefile automation for all tasks
- **📋 Production Ready**: Complete Kubernetes manifests included
- **🔒 Secure Defaults**: Non-root container, minimal image, proper RBAC

### Available Make Targets

```bash
make help          # Show all available targets
make build         # Build binary
make test          # Run all tests
make docker-build  # Build container image
make clean         # Clean build artifacts
make fmt           # Format code
make lint          # Run linter
make coverage      # Generate coverage report
```

## 🤝 Contributing

1. Fork the repository
2. Create a feature branch: `git checkout -b feature/amazing-feature`
3. Run tests: `make test`
4. Commit changes: `git commit -m 'Add amazing feature'`
5. Push to branch: `git push origin feature/amazing-feature`
6. Open a Pull Request

## 📄 License

Licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE) for details.

## 🙏 Acknowledgments

- Built using the [Kubernetes Scheduler Framework](https://kubernetes.io/docs/concepts/scheduling-eviction/scheduling-framework/)
- Inspired by production workload optimization needs
