#!/bin/bash
# Remote environment setup for Claude Code web sessions
# This script configures DNS and installs mise in cloud containers

set -e

# Only run in remote (web/cloud) environments
if [ "$CLAUDE_CODE_REMOTE" != "true" ]; then
  exit 0
fi

echo "Setting up remote environment..."

# 1. Configure DNS (required in web containers)
echo "Configuring DNS..."
echo "nameserver 8.8.8.8" | sudo tee /etc/resolv.conf > /dev/null

# 2. Install mise if not already installed
if ! command -v mise &> /dev/null; then
  echo "Installing mise..."
  curl -fsSL https://mise.run | sh

  # Activate mise for this session
  export PATH="$HOME/.local/bin:$PATH"
  eval "$(mise activate bash)"
fi

# 3. Trust mise config and install tools
echo "Installing project tools..."
cd "$CLAUDE_PROJECT_DIR"
mise trust
mise install

echo "Remote environment setup complete!"
