# Fastest Empty Node Scheduler - Examples

This directory contains example Kubernetes workloads that demonstrate how to use the **FastestEmptyNode** scheduler plugin effectively.

## 🎯 Prerequisites

1. **Deploy the scheduler** to your cluster:
   ```bash
   make deploy
   ```

2. **Verify the scheduler** is running:
   ```bash
   kubectl get pods -n kube-system -l app=fastest-empty-node-scheduler
   ```

3. **Check scheduler logs** (optional):
   ```bash
   make logs
   ```

## 📋 Available Examples

| Example | Use Case | Duration | Description |
|---------|----------|----------|-------------|
| [`basic-workload.yaml`](./basic-workload.yaml) | Simple Job | 5 minutes | Basic example with job duration annotation |
| [`batch-job.yaml`](./batch-job.yaml) | Batch Processing | 10 minutes | CronJob for regular batch processing |
| [`ml-training.yaml`](./ml-training.yaml) | ML Training | 2 hours | Long-running machine learning training job |
| [`web-service.yaml`](./web-service.yaml) | Web Service | 8 hours | Predictable web service workload |
| [`data-pipeline.yaml`](./data-pipeline.yaml) | Data Processing | 30 minutes | ETL pipeline with known duration |

## 🚀 How to Run Examples

### Run a Single Example
```bash
# Apply any example
kubectl apply -f examples/basic-workload.yaml

# Check where it was scheduled
kubectl get pods -o wide

# View scheduler decisions in logs
make logs
```

### Run Multiple Examples Together
```bash
# Apply all examples to see scheduling in action
kubectl apply -f examples/

# Watch scheduling decisions
kubectl get pods -o wide --watch
```

### Clean Up Examples
```bash
# Remove all example workloads
kubectl delete -f examples/ --ignore-not-found
```

## 🎯 Understanding Scheduling Decisions

### Key Annotation
All workloads must include the duration annotation:
```yaml
metadata:
  annotations:
    scheduling.workload.io/expected-duration-seconds: "300"  # Expected runtime in seconds
```

### Scheduler Selection
Specify our custom scheduler:
```yaml
spec:
  schedulerName: fastest-empty-node-scheduler
```

### Scheduling Logic
The scheduler will:
1. **Calculate node completion times** based on existing workloads
2. **Score nodes** using: `(timeScore × 100) + balanceScore`
3. **Select the node** that becomes available soonest
4. **Use pod capacity** as a tie-breaker

## 📊 Example Scenarios

### Scenario 1: Empty vs Busy Nodes
```bash
# 1. Apply a long-running job
kubectl apply -f examples/ml-training.yaml

# 2. Apply a short job - should avoid the ML training node
kubectl apply -f examples/basic-workload.yaml

# 3. Check placement
kubectl get pods -o wide
```

### Scenario 2: Capacity-Based Tie Breaking
```bash
# 1. Apply multiple jobs with same duration
kubectl apply -f examples/batch-job.yaml
kubectl apply -f examples/data-pipeline.yaml

# 2. Check how scheduler balances load across nodes
kubectl get pods -o wide
```

### Scenario 3: Mixed Workload Performance
```bash
# 1. Apply all examples at once
kubectl apply -f examples/

# 2. Watch scheduling decisions
kubectl get pods -o wide --watch

# 3. Analyze scheduler logs
make logs
```

## 🔍 Monitoring and Debugging

### View Scheduler Decisions
```bash
# Real-time scheduler logs
make logs

# Get specific pod scheduling events
kubectl describe pod <pod-name>

# Check node resource usage
kubectl describe nodes
```

### Common Patterns You'll See
- **Empty nodes preferred** for immediate scheduling
- **Nodes finishing soon** chosen over long-running nodes
- **Balanced distribution** when completion times are equal
- **Resource-aware placement** respecting node capacity

## 💡 Creating Your Own Examples

### Template for New Workloads
```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: my-custom-job
  annotations:
    scheduling.workload.io/expected-duration-seconds: "900"  # 15 minutes
spec:
  template:
    spec:
      schedulerName: fastest-empty-node-scheduler
      restartPolicy: Never
      containers:
      - name: worker
        image: busybox
        command: ["sleep", "900"]
        resources:
          requests:
            cpu: "100m"
            memory: "128Mi"
          limits:
            cpu: "500m"
            memory: "512Mi"
```

### Best Practices
1. **Always include duration annotation** - Required for scheduling logic
2. **Set realistic durations** - Helps scheduler make better decisions
3. **Include resource requests** - Enables proper resource planning
4. **Use appropriate job types** - Job, CronJob, Deployment based on use case

## 📚 Additional Resources

- [Main README](../README.md) - Complete project documentation
- [Deployment Guide](../deploy/) - Production deployment instructions
- [Makefile Commands](../Makefile) - Available automation commands

## 🤝 Contributing Examples

Have a great use case for our scheduler? Please contribute:

1. Create your example following the template above
2. Add it to this README with description
3. Test it works with `make test`
4. Submit a pull request

**Happy Scheduling!** 🎯
