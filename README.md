# 🚀 Chronos Kubernetes Scheduler

[![Tests](https://github.com/rohitraut3366/chronos-kubernetes-scheduler/workflows/Tests/badge.svg?branch=main)](https://github.com/rohitraut3366/chronos-kubernetes-scheduler/actions?query=branch%3Amain)
[![Coverage](https://img.shields.io/badge/coverage-88%25-brightgreen.svg)](https://github.com/rohitraut3366/chronos-kubernetes-scheduler/actions?query=branch%3Amain)
[![Go Report Card](https://goreportcard.com/badge/github.com/rohitraut3366/chronos-kubernetes-scheduler)](https://goreportcard.com/report/github.com/rohitraut3366/chronos-kubernetes-scheduler)

**Advanced Kubernetes custom scheduler plugin with dual scheduling modes:** intelligently bin-packs workloads based on job durations using either **individual pod-by-pod scheduling** or **high-performance batch grouping**, maximizing cluster resource utilization and cost optimization.

## 🎯 Overview

The **Chronos** scheduler plugin uses job duration annotations to intelligently bin-pack workloads with **two powerful scheduling modes**:

### 📝 Individual Mode (Default)
**Traditional pod-by-pod scheduling** with intelligent bin-packing:
- **Bin-Packing First**: Fits new jobs into existing time windows when possible
- **Maximizes Consolidation**: Prefers nodes with longer remaining work for better packing  
- **Minimizes Extensions**: When jobs must extend beyond existing work, chooses minimal extension
- **Avoids Empty Nodes**: Heavily penalizes empty nodes to enable cluster cost optimization
- **Immediate Scheduling**: Low-latency decisions for interactive workloads

### 🚀 Grouping Mode (High-Volume)  
**Batch scheduling with advanced grouping optimization**:
- **CRON-Based Batching**: Collects pods every 5 seconds (configurable)
- **LPT Heuristic**: Longest Processing Time first for optimal bin-packing
- **Resource-Aware**: Respects CPU/Memory constraints and node capacity
- **Kubernetes-Native**: Supports NodeSelector, Taints/Tolerations, Affinity
- **Skew Minimization**: Optimizes per-node completion time balance
- **High-Volume Ready**: Handles 2000+ pods in 15 seconds efficiently

## 🏗️ Architecture

```
chronos-kubernetes-scheduler/
├── cmd/
│   └── scheduler/
│       └── main.go                    # Application entry point
├── internal/
│   └── scheduler/
│       ├── plugin.go                  # Core scheduler plugin & individual mode
│       ├── batch_scheduler.go         # Grouping mode & batch scheduling
│       ├── plugin_test.go             # Unit tests (43+ test functions)
│       ├── batch_scheduler_test.go    # Batch scheduler tests
│       └── plugin_integration_test.go # Integration tests
├── docs/
│   └── GROUPING_FEATURE.md            # Comprehensive grouping guide
├── charts/
│   └── chronos-kubernetes-scheduler/  # Helm chart for deployment
│       ├── Chart.yaml
│       ├── values.yaml
│       └── templates/                 # Kubernetes manifests
├── examples/
│   ├── resource-aware-demo.yaml       # Resource-aware workloads
│   └── grouping-configuration.yaml    # Grouping mode examples
├── build/
│   └── Dockerfile                     # Multi-stage container build
├── chronos-analyzer.sh                # Scheduler analysis tool
├── Makefile                           # Build automation
└── go.mod                             # Go module definition
```

### 🎛️ Component Overview

| Component | Purpose | Mode |
|-----------|---------|------|
| **plugin.go** | Core scheduler plugin, individual scoring | Individual Mode |
| **batch_scheduler.go** | Batch processing, grouping optimization | Grouping Mode |
| **CHRONOS_GROUPING_ENABLED** | Feature flag controlling mode selection | Both |

## ⚖️ Mode Comparison

| Feature | Individual Mode | Grouping Mode |
|---------|----------------|---------------|
| **Activation** | `CHRONOS_GROUPING_ENABLED=false` (default) | `CHRONOS_GROUPING_ENABLED=true` |
| **Best For** | Low-volume, interactive workloads | High-volume, batch processing |
| **Throughput** | ~100 pods/minute | ~2000 pods/15 seconds |
| **Latency** | ~50ms per pod | ~5s batch interval |
| **Resource Usage** | Lower, steady | Higher during batching |
| **Bin-Packing** | Good | Excellent |
| **Use Cases** | Web apps, APIs, mixed workloads | ML training, data processing, jobs |

**💡 Tip**: Start with Individual Mode for most workloads, switch to Grouping Mode for high-volume scenarios.

## 🚀 Quick Start

### Prerequisites
- Go 1.22+
- Docker (for container builds)
- Kubernetes 1.28+ (for deployment)

### Build & Test

```bash
# Run all tests (unit + integration + performance)
make test

# Build binary
make build

# Build Docker image
make docker-build

# Run all checks (format, vet, lint, test, build)
make all
```

### Deploy to Kubernetes

#### Individual Mode (Default)
```bash
# Deploy using Helm (individual mode by default)
helm install chronos-scheduler ./charts/chronos-kubernetes-scheduler

# Or explicitly set individual mode
helm install chronos-scheduler ./charts/chronos-kubernetes-scheduler \
  --set env.CHRONOS_GROUPING_ENABLED=false
```

#### Grouping Mode (High-Volume)
```bash
# Deploy in grouping mode for high-volume workloads
helm install chronos-scheduler ./charts/chronos-kubernetes-scheduler \
  --set env.CHRONOS_GROUPING_ENABLED=true \
  --set env.BATCH_INTERVAL_SECONDS=5 \
  --set env.BATCH_SIZE_LIMIT=0
```

#### Verify Installation
```bash
# Check deployment status  
kubectl get pods -l app.kubernetes.io/name=chronos-kubernetes-scheduler

# View logs (check for mode confirmation)
kubectl logs -l app.kubernetes.io/name=chronos-kubernetes-scheduler

# Look for: "📝 INDIVIDUAL MODE enabled" or "🚀 GROUPING MODE enabled"

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

### 🚩 Grouping Feature Flag

Chronos supports two scheduling modes controlled by the `CHRONOS_GROUPING_ENABLED` environment variable:

| Mode | Flag Value | Description |
|------|------------|-------------|
| **Individual** (default) | `false` or unset | Traditional pod-by-pod scoring |
| **Grouping** | `true` | Batch scheduling with grouping optimization |

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: chronos-scheduler
spec:
  template:
    spec:
      containers:
      - name: chronos-scheduler
        image: chronos:latest
        env:
        - name: CHRONOS_GROUPING_ENABLED
          value: "true"  # Enable batch grouping mode
        - name: BATCH_INTERVAL_SECONDS
          value: "5"     # Batch every 5 seconds
        - name: BATCH_SIZE_LIMIT
          value: "0"     # No limit (unlimited batch size)
```

**When to use grouping mode:**
- ✅ High-volume workloads (2000+ pods in 15 seconds)
- ✅ Batch processing jobs (ML training, data processing)
- ✅ When optimizing cluster utilization is critical

See [docs/GROUPING_FEATURE.md](docs/GROUPING_FEATURE.md) for detailed configuration guide.

### Plugin Constants

```go
const (
    PluginName            = "Chronos"
    JobDurationAnnotation = "scheduling.workload.io/expected-duration-seconds"
    maxPossibleScore      = 100000000  // ~3.17 years, excellent granularity
)
```

## 📈 Performance & Monitoring

- **Scoring Speed**: ~800ns per node (tested up to 50 nodes)
- **Memory Usage**: Minimal overhead over default scheduler
- **Scalability**: Tested with 100 jobs across 20 nodes
- **Accuracy**: 96%+ scheduling correctness in realistic scenarios

### Performance Analysis Tools

```bash
# Comprehensive scheduler analysis
./chronos-analyzer.sh <pod-namespace> [scheduler-namespace]

# Monitor real-time scheduling
kubectl logs -l app.kubernetes.io/name=chronos-kubernetes-scheduler --tail=100 -f

# Check scheduler metrics (if ServiceMonitor enabled)
curl http://scheduler-service:10259/metrics
```

### Integration Testing

```bash
# Run full integration tests with K3s
make integration-setup

# Quick integration test in existing cluster
make integration-quick
```

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
- Tested with comprehensive Go testing best practices
