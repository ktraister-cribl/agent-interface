#!/bin/bash
set -euo pipefail

echo "=== Agent Interface: macOS dependency installer ==="
echo ""

# Xcode CLI tools (needed by Fyne / CGO)
if ! xcode-select -p &>/dev/null; then
    echo "Installing Xcode Command Line Tools..."
    xcode-select --install
    echo "Re-run this script after the Xcode tools install finishes."
    exit 0
else
    echo "Xcode CLI tools found."
fi

# Homebrew
if ! command -v brew &>/dev/null; then
    echo "Homebrew not found. Install it from https://brew.sh and re-run."
    exit 1
fi

# System packages
echo "Installing system packages via Homebrew..."
brew install sox go pipx

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
export PATH="$PATH:$(go env GOPATH)/bin"

# Bucky / whisper.cpp
echo "Installing Bucky (whisper.cpp Go bindings)..."
go install github.com/ardanlabs/bucky@latest
mkdir -p ~/.local/share/agent-interface/lib
bucky install -lib ~/.local/share/agent-interface/lib
bucky model get base.en
mkdir -p ~/.local/share/agent-interface/models
mv ~/models/ggml-base.en.bin ~/.local/share/agent-interface/models/

echo ""
echo "=== Done! ==="
echo ""
echo "Make sure Go binaries are on your PATH. Add to your shell profile (~/.zshrc):"
echo '  export PATH="$PATH:$(go env GOPATH)/bin"'
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
echo ""
echo "=== Optional: Global hotkey ==="
echo ""
echo "The in-app Ctrl+Space hotkey works when the window is focused."
echo "For a system-wide hotkey, install skhd:"
echo ""
echo "  brew install koekeishiya/formulae/skhd"
echo "  skhd --start-service"
echo ""
echo "Add to ~/.skhdrc:"
echo '  ctrl - space : pkill -USR1 -f agent-interface'
echo ""
echo "Note: macOS will prompt for microphone and accessibility permissions"
echo "on first use. Grant them when asked."
