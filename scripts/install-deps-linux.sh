#!/bin/bash
set -euo pipefail

echo "=== Agent Interface: Linux (Ubuntu) dependency installer ==="
echo ""

# System packages
echo "Installing system packages..."
sudo apt update
sudo apt install -y gcc g++ make libgl1-mesa-dev xorg-dev libwayland-dev libxkbcommon-dev alsa-utils sox pipx

# Go
if ! command -v go &>/dev/null; then
    echo "Installing Go 1.26..."
    curl -LO https://go.dev/dl/go1.26.2.linux-amd64.tar.gz
    sudo rm -rf /usr/local/go
    sudo tar -C /usr/local -xzf go1.26.2.linux-amd64.tar.gz
    rm go1.26.2.linux-amd64.tar.gz
    export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin
    echo ""
    echo "Add to ~/.bashrc:"
    echo '  export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin'
    echo ""
else
    echo "Go already installed: $(go version)"
fi

# Piper TTS
echo "Installing Piper TTS..."
pipx install piper-tts

# Voice
echo "Downloading default Piper voice (Ryan medium)..."
mkdir -p ~/.local/share/piper/voices
curl -L -o ~/.local/share/piper/voices/en_US-ryan-medium.onnx \
    https://huggingface.co/rhasspy/piper-voices/resolve/main/en/en_US/ryan/medium/en_US-ryan-medium.onnx
curl -L -o ~/.local/share/piper/voices/en_US-ryan-medium.onnx.json \
    https://huggingface.co/rhasspy/piper-voices/resolve/main/en/en_US/ryan/medium/en_US-ryan-medium.onnx.json

# Ensure GOBIN is on PATH
export PATH="$PATH:/usr/local/go/bin:$(go env GOPATH)/bin"

# Bucky / whisper.cpp
echo "Installing Bucky (whisper.cpp Go bindings)..."
go install github.com/ardanlabs/bucky@latest
mkdir -p ~/.local/share/agent-interface/lib
bucky install -lib ~/.local/share/agent-interface/lib
bucky model get base.en
mkdir -p ~/.local/share/agent-interface/models
mv ~/models/ggml-base.en.bin ~/.local/share/agent-interface/models/

# keyd (global hotkey)
echo "Installing keyd for global hotkey support..."
sudo apt install -y keyd
sudo systemctl enable --now keyd

if [ ! -f /etc/keyd/default.conf ]; then
    echo "Creating keyd config..."
    sudo tee /etc/keyd/default.conf >/dev/null <<'CONF'
[ids]
*

[main]

[control]
space = command(pkill -USR1 -f agent-interface)
CONF
    sudo keyd reload
    echo "Global hotkey Ctrl+Space configured via keyd."
else
    echo "keyd config already exists — skipping. Add this to [control] section manually:"
    echo '  space = command(pkill -USR1 -f agent-interface)'
fi

echo ""
echo "=== Done! ==="
echo ""
echo "Create ~/.agent-interface.toml. Example for Brave Search:"
echo ""
echo '  backend        = "brave"'
echo '  brave_api_key  = "YOUR_KEY"'
echo '  bucky_lib      = "'$HOME'/.local/share/agent-interface/lib"'
echo '  whisper_model  = "'$HOME'/.local/share/agent-interface/models/ggml-base.en.bin"'
echo '  piper_model    = "'$HOME'/.local/share/piper/voices/en_US-ryan-medium.onnx"'
echo ""
echo "Or for Claude via Bedrock proxy:"
echo ""
echo '  backend            = "claude"'
echo '  claude_base_url    = "https://your-proxy/bedrock"'
echo '  claude_model       = "us.anthropic.claude-sonnet-4-6"'
echo '  claude_api_key     = "-"'
echo '  claude_use_bedrock = true'
echo '  bucky_lib          = "'$HOME'/.local/share/agent-interface/lib"'
echo '  whisper_model      = "'$HOME'/.local/share/agent-interface/models/ggml-base.en.bin"'
echo '  piper_model        = "'$HOME'/.local/share/piper/voices/en_US-ryan-medium.onnx"'
echo ""
echo "Then: cd agent-interface && go install . && agent-interface"
