# 🚀 K9s Plugin Usage Guide

## ✅ Installation Complete
Your Chronos scheduling analysis plugin is now installed in k9s!

**Installed Files:**
```
~/.config/k9s/
├── plugins/
│   ├── chronos-pod-decision.sh     # Live log analysis script  
│   └── show-json-decision.sh       # JSON analysis script
├── plugin.yml                      # Plugin configuration
└── scheduler_analysis_*.json       # Analysis data file
```

## 🎯 How to Use in K9s UI

### Step-by-Step Instructions:

1. **Start K9s**
   ```bash
   k9s
   ```

2. **Navigate to Pods**
   - Press `:` to open command mode
   - Type `pods` and press Enter
   - OR press `Shift+:` and type `po` for pods view

3. **Select Any Pod**
   - Use arrow keys ↑↓ to navigate
   - Select any pod in the list

4. **Trigger the Plugin**
   - Press `Ctrl+D` (while pod is selected)
   - A new terminal window will open showing scheduling analysis

5. **View Rich Analysis Data**
   You'll see:
   ```
   📋 SCHEDULING DECISION SUMMARY
   ════════════════════════════════════════════════════════════════
   Pod Name:            namespace/pod-name
   Pod Duration:        1703s
   Chosen Node:         ip-10-10-163-250.us-west-2.compute.internal
   Nodes Evaluated:     37
   ════════════════════════════════════════════════════════════════

   🎯 NODE EVALUATION DETAILS:
   ┌─────────────────────────────────┬─────────────┬─────────────────┬────────────┬────────────┐
   │ Node Name                       │ Strategy    │ Completion Time │ Raw Score  │ Norm Score │
   ├─────────────────────────────────┼─────────────┼─────────────────┼────────────┼────────────┤
   │ ip-10-10-163-250 (CHOSEN)      │ BIN-PACKING │ 1761s           │  1,176,100 │        100 │
   │ ip-10-10-173-8                  │ EXTENSION   │ 1494s           │     79,100 │         11 │
   │ ...                             │ ...         │ ...             │ ...        │ ...        │
   └─────────────────────────────────┴─────────────┴─────────────────┴────────────┴────────────┘
   ```

6. **Press Any Key to Continue**
   - The analysis window will wait for you to review
   - Press any key to return to k9s

## 🔧 Plugin Features

- **🎯 Smart Detection**: Automatically uses JSON analysis files when available
- **📊 Rich Data**: Shows pod name, duration, all 37 nodes evaluated with scores
- **✅ Highlighted Choice**: Chosen node highlighted in green with reasoning
- **🔄 Fallback**: Uses live logs if no JSON analysis available

## 💡 Tips

1. **For Best Results**: Place `scheduler_analysis_*.json` files in `~/.config/k9s/`
2. **Quick Access**: Remember `Ctrl+D` shortcut for any selected pod
3. **Multiple Files**: Plugin automatically uses the most recent JSON analysis file

## 🛠️ Troubleshooting

**Plugin doesn't show up?**
- Restart k9s completely
- Check that `plugin.yml` exists in `~/.config/k9s/`

**No analysis data?**
- Ensure JSON files are in `~/.config/k9s/` directory
- Plugin will fall back to live log analysis

**Shortcut not working?**
- Make sure a pod is selected (highlighted)
- Try pressing `Ctrl+D` firmly

## 🔍 Available Shortcuts in K9s

- `:pods` or `:po` - Go to pods view
- `Ctrl+D` - Show Chronos scheduling decision (our plugin!)
- `Enter` - Describe selected resource
- `l` - Show logs
- `d` - Describe
- `e` - Edit resource
- `?` - Show all available shortcuts
