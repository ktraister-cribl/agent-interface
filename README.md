# Brave Agent Interface

Voice-first AI assistant. Talk to it, it talks back. Built with Go, Fyne, whisper.cpp, and Piper TTS. Uses the Brave Search API for privacy-focused responses.

## Requirements

- Ubuntu 26.04 LTS (x86_64)
- Go 1.26+
- A Brave Search API key (Answers plan)
- A microphone and speakers

## Setup

### 1. System dependencies

```bash
sudo apt update
sudo apt install -y gcc g++ make libgl1-mesa-dev xorg-dev libwayland-dev libxkbcommon-dev alsa-utils sox

2. Install Go
curl -LO https://go.dev/dl/go1.26.2.linux-amd64.tar.gz
sudo rm -rf /usr/lib/go-1.26
sudo tar -C /usr/local -xzf go1.26.2.linux-amd64.tar.gz

Add to ~/.bashrc:

export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin

3. Install Piper TTS
pipx install piper-tts

Download a voice:

mkdir -p ~/.local/share/piper/voices
cd ~/.local/share/piper/voices

# Ryan (medium, US English) — good default
curl -LO https://huggingface.co/rhasspy/piper-voices/resolve/main/en/en_US/ryan/medium/en_US-ryan-medium.onnx
curl -LO https://huggingface.co/rhasspy/piper-voices/resolve/main/en/en_US/ryan/medium/en_US-ryan-medium.onnx.json

Browse more voices: https://rhasspy.github.io/piper-samples/

4. Install Bucky (whisper.cpp)
go install github.com/ardanlabs/bucky@latest

mkdir -p ~/.local/share/agent-interface/lib
bucky install -lib ~/.local/share/agent-interface/lib

bucky model get base.en
mkdir -p ~/.local/share/agent-interface/models
mv ~/models/ggml-base.en.bin ~/.local/share/agent-interface/models/

5. Install keyd (global hotkey)
sudo apt install keyd
sudo systemctl enable --now keyd

Create /etc/keyd/default.conf:

[ids]

*

[main]

[control]
space = command(pkill -USR1 -f agent-interface)

sudo keyd reload

This binds Ctrl+Space globally to trigger the app's recording, even when the window isn't focused.

6. Environment variables
Add to ~/.bashrc:

export BUCKY_LIB=$HOME/.local/share/agent-interface/lib
export BUCKY_TEST_MODEL=$HOME/.local/share/agent-interface/models/ggml-base.en.bin
export BRAVE_API_KEY=your-key-here

7. Go dependencies and build
cd ~/git/agent-interface
go get fyne.io/fyne/v2@latest
go get github.com/openai/openai-go/v3
go get github.com/ardanlabs/bucky
go install .

```

### Run
agent-interface

First build takes ~2–5 minutes (GLFW C compilation). Subsequent go install . builds: ~3–10s.

### Usage
Action	How
Talk	Press Ctrl+Space (global, works from any window) or click Talk
Stop speaking	Click Stop
Type instead	Use the text input + Send
Toggle voice response	Voice response checkbox
Change hotkey	Configure button
Copy conversation text	Click-drag to select, Ctrl+C

Beeps: 800Hz = recording started, 400Hz = recording stopped.

Configuration
Variable	Purpose	Default
BUCKY_LIB	Path to whisper.cpp shared libs	Required
BUCKY_TEST_MODEL	Path to whisper model (.bin)	Required
BRAVE_API_KEY	Brave Search API key	Required

Piper voice is set in main.go:

piperModel = os.ExpandEnv("$HOME/.local/share/piper/voices/en_US-ryan-medium.onnx")

### Architecture
Mic → arecord (5s raw PCM S16_LE)
    → S16_LE → float32 conversion
    → whisper.cpp (bucky) transcription
    → Brave Search API (single-turn, streaming)
    → cleanForSpeech (strip markdown, usage JSON)
    → Piper TTS (raw PCM 22050Hz)
    → aplay

Global hotkey path:

Ctrl+Space → keyd (evdev) → pkill -USR1 → Go signal handler → triggerTalk()

### Troubleshooting
Symptom	Fix
wayland-client-core.h: No such file	sudo apt install libwayland-dev libxkbcommon-dev
piper: Unable to find voice	Download the .onnx + .onnx.json to the path in main.go
Fyne fyne.DoAndWait errors	All widget calls from goroutines must be wrapped in fyne.DoAndWait
arecord eats your keystrokes	Set cmd.Stdin = nil on the arecord exec
Piper sounds robotic	Try a different voice (see piper-samples page)
Hotkey not firing	keyd monitor to verify keyd sees the keypress; check sudo systemctl status keyd
play silent	sudo apt install libsox-fmt-all
GNOME eating Ctrl+Space	gsettings set org.gnome.desktop.wm.keybindings switch-input-source "[]"

### Notes
The Brave Search API is single-turn only (1 user message, no system/assistant roles). Multi-turn memory would require a different LLM backend.
base.en whisper model is ~400ms per 5s clip on CPU. Use small.en for better accuracy at ~1s latency.
Piper medium voices are ~200–400ms per sentence on CPU. high voices are ~1s+.
Total round-trip latency: ~5s (recording) + ~0.5s (STT) + ~1–3s (API) + ~0.5–1s (TTS) = ~7–10s
The app traps SIGUSR1 for the global hotkey. Any kill -USR1 $(pidof agent-interface) triggers a talk cycle.
