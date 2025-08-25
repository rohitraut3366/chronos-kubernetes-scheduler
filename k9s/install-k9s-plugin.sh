#!/bin/bash

# K9s Chronos Plugin Installer
# Installs the enhanced scheduling analysis plugin for k9s

set -e

GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

echo -e "${BLUE}🚀 Installing Chronos K9s Plugin...${NC}\n"

# Check if k9s config and plugins directories exist
K9S_CONFIG="$HOME/.config/k9s"
K9S_PLUGINS="$K9S_CONFIG/plugins"

if [[ ! -d "$K9S_CONFIG" ]]; then
    echo -e "${YELLOW}📁 Creating k9s config directory: $K9S_CONFIG${NC}"
    mkdir -p "$K9S_CONFIG"
fi

if [[ ! -d "$K9S_PLUGINS" ]]; then
    echo -e "${YELLOW}📁 Creating k9s plugins directory: $K9S_PLUGINS${NC}"
    mkdir -p "$K9S_PLUGINS"
fi

# Current script directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo -e "${BLUE}📋 Installing plugin files...${NC}"

# Copy scripts from project plugins directory
cp "$SCRIPT_DIR/plugins/chronos-pod-decision.sh" "$K9S_PLUGINS/"
cp "$SCRIPT_DIR/plugins/show-json-decision.sh" "$K9S_PLUGINS/"
echo "✅ Copied analysis scripts to $K9S_PLUGINS/"

# Make scripts executable
chmod +x "$K9S_PLUGINS/chronos-pod-decision.sh"
chmod +x "$K9S_PLUGINS/show-json-decision.sh"
echo "✅ Made scripts executable"

# Handle plugin configuration
if [[ -f "$K9S_CONFIG/plugin.yml" ]]; then
    echo -e "${YELLOW}⚠️  Existing plugin.yml found${NC}"
    cp "$K9S_CONFIG/plugin.yml" "$K9S_CONFIG/plugin.yml.backup"
    echo "✅ Backed up existing plugin.yml to plugin.yml.backup"
fi

# Merge or create plugin configuration
if [[ -f "$K9S_CONFIG/plugin.yml" ]]; then
    # Add our plugin to existing config
    echo -e "\n# Chronos Scheduling Analysis Plugin" >> "$K9S_CONFIG/plugin.yml"
    tail -n +4 "$SCRIPT_DIR/plugins.yaml" >> "$K9S_CONFIG/plugin.yml"
    echo "✅ Added Chronos plugin to existing plugin.yml"
else
    # Create new plugin config
    cp "$SCRIPT_DIR/plugins.yaml" "$K9S_CONFIG/plugin.yml"
    echo "✅ Created new plugin.yml"
fi

echo -e "\n${GREEN}🎉 Installation complete!${NC}\n"

echo -e "${BLUE}📖 Usage Instructions:${NC}"
echo "1. Restart k9s"
echo "2. Navigate to any pod"
echo "3. Press Ctrl-D to see scheduling analysis"
echo ""

echo -e "${BLUE}💡 Pro Tips:${NC}"
echo "• The plugin uses live log analysis for real-time scheduling decisions"
echo "• Make sure your Chronos scheduler is running and logging to kubectl logs"
echo "• The analysis shows all node evaluations with scores and completion times"
echo ""

echo -e "${YELLOW}🔄 Next Steps:${NC}"
echo "1. Restart k9s"
echo "2. Test the plugin: k9s → select pod → Ctrl-D"
echo "3. Enjoy real-time scheduling analysis!"
