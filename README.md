# Brave Agent Interface

In order to make vocal use of the Brave privacy-focused AI, use this compatability layer

```
# agent-interface dependencies — Ubuntu 26.04

# System packages
sudo apt update
sudo apt install -y gcc g++ make libgl1-mesa-dev xorg-dev libwayland-dev libxkbcommon-dev alsa-utils

# Go (official tarball, not apt)
curl -LO https://go.dev/dl/go1.26.2.linux-amd64.tar.gz
sudo rm -rf /usr/lib/go-1.26
sudo tar -C /usr/local -xzf go1.26.2.linux-amd64.tar.gz
# add to ~/.bashrc:
# export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin

# Piper TTS
pipx install piper-tts
mkdir -p ~/.local/share/piper/voices
cd ~/.local/share/piper/voices
curl -LO https://huggingface.co/rhasspy/piper-voices/resolve/main/en/en_US/ryan/medium/en_US-ryan-medium.onnx
curl -LO https://huggingface.co/rhasspy/piper-voices/resolve/main/en/en_US/ryan/medium/en_US-ryan-medium.onnx.json

# Bucky (whisper.cpp)
go install github.com/ardanlabs/bucky@latest
mkdir -p ~/.local/share/agent-interface/lib
bucky install -lib ~/.local/share/agent-interface/lib
bucky model get base.en
mkdir -p ~/.local/share/agent-interface/models
mv ~/models/ggml-base.en.bin ~/.local/share/agent-interface/models/

# Env vars — add to ~/.bashrc
export BUCKY_LIB=$HOME/.local/share/agent-interface/lib
export BUCKY_TEST_MODEL=$HOME/.local/share/agent-interface/models/ggml-base.en.bin
export BRAVE_API_KEY=your-key-here

# Go module deps
cd ~/git/agent-interface
go get fyne.io/fyne/v2@latest
go get github.com/openai/openai-go/v3
go get github.com/ardanlabs/bucky
```
