# K9s DURATION Column for Chronos Scheduler

Add a **DURATION column** to K9s pods view to instantly see which pods are managed by Chronos scheduler and their expected durations.

## 🎯 What You Get

Your K9s pods view gets a **DURATION column** showing:
- ✅ **Expected duration** for Chronos-scheduled pods (e.g., `300s`, `1800s`)
- ✅ **Empty/blank** for regular pods
- ✅ **Instant visibility** into which workloads Chronos is managing

## 📊 Example

```
NAMESPACE  NAME                    PF  READY  STATUS   DURATION  NODE              AGE
default    buildkite-agent-xyz     ●   1/1    Running     1800s  ip-10-10-178-150   5m
default    nginx-regular           ●   1/1    Running            ip-10-10-178-151  15m
```

## 🚀 Installation

### For macOS:
```bash
# Set K9s config directory (required on macOS)
export XDG_CONFIG_HOME=$HOME/.config
echo 'export XDG_CONFIG_HOME=$HOME/.config' >> ~/.zshrc

# Install configuration
mkdir -p ~/.config/k9s
cp k9s/views/views.yml ~/.config/k9s/views.yml

# Restart K9s
k9s
```

### For Linux:
```bash
# Install configuration
mkdir -p ~/.config/k9s
cp k9s/views/views.yml ~/.config/k9s/views.yml

# Restart K9s
k9s
```

## 📚 Requirements

- **K9s v0.40+** (check with `k9s version`)
- **Chronos scheduler** running
- **Pods with annotation**: `scheduling.workload.io/expected-duration-seconds`

## 🐛 Quick Troubleshooting

**DURATION column not showing?**
1. Check K9s version: `k9s version` (need v0.40+)
2. Restart K9s after copying configuration
3. On macOS, make sure `XDG_CONFIG_HOME` is set

**DURATION shows empty for all pods?**
- This is normal! Only pods with the Chronos annotation will show values
- Regular Kubernetes pods will show blank in DURATION column

## ✨ Benefits

- **Zero complexity** - just one configuration file
- **No performance impact** - native K9s column enhancement  
- **Always visible** - see duration info in your normal workflow
- **Clean separation** - instantly identify Chronos vs regular workloads

## 🔍 Optional: Pod-Specific Scheduling Analysis

For deeper analysis of individual pod scheduling decisions:

### 📱 K9s Integration (Recommended)

Install the k9s plugin that provides real-time scheduling analysis:

**Easy Installation (Recommended):**
```bash
# Run the automated installer
./k9s/install-k9s-plugin.sh

# Or force reinstall (cleans up any duplicates)
./k9s/install-k9s-plugin.sh --force

# Restart K9s and start analyzing!
```

**Manual Installation:**
```bash
# Create plugins directory and install files
mkdir -p ~/.config/k9s/plugins
cp k9s/plugins/chronos-pod-decision.sh ~/.config/k9s/plugins/
cp k9s/plugins/show-json-decision.sh ~/.config/k9s/plugins/
cp k9s/plugins.yaml ~/.config/k9s/plugin.yml
chmod +x ~/.config/k9s/plugins/*.sh

# Restart K9s
```

**Usage:** 
1. Start k9s: `k9s`
2. Navigate to pods: `:pods` or `:po`  
3. Select any pod with arrow keys
4. Press `w` to see scheduling analysis (Where/What node)

**What you'll see:**
- 📱 **Live Analysis**: Real-time analysis from scheduler logs
- 📊 **Rich Data**: Pod name, duration, all nodes evaluated with scores
- ✅ **Highlighted Choice**: Chosen node with reasoning

**The plugin shows:**
```
┌─────────────────────────────────────┬─────────────┬─────────────────┬────────────┬────────────┐
│ Node Name                           │ Strategy    │ Completion Time │ Raw Score  │ Norm Score │
├─────────────────────────────────────┼─────────────┼─────────────────┼────────────┼────────────┤
│ ip-10-10-178-150                    │ BIN-PACKING │ 245s            │ 1250       │ 85         │
│ ip-10-10-178-151                    │ EXTENSION   │ 890s            │ 450        │ 62         │
└─────────────────────────────────────┴─────────────┴─────────────────┴────────────┴────────────┘
✅ Chosen Node: ip-10-10-178-150
🎯 Strategy Used: BIN-PACKING
```
