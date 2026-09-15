# Agent Interface

Voice-first AI assistant for Linux and macOS. Talk to it, it talks back. Built with Go, Fyne, whisper.cpp, and Piper TTS. Supports two LLM backends: Brave Search API (privacy-focused web answers) and Claude via Amazon Bedrock (multi-turn conversational AI).


<img width="608" height="547" alt="Screenshot From 2026-09-14 19-55-39" src="https://github.com/user-attachments/assets/936340a1-1f31-4836-a59b-e6ad2294b6fa" />


## Requirements

- Linux (Ubuntu 26.04+ x86_64) or macOS (13+ x86_64/arm64)
- Go 1.26+
- A microphone and speakers
- One of:
  - A Brave Search API key (Answers plan)
  - Access to Claude via Amazon Bedrock (directly or through a proxy)


## Quick Setup

Install scripts handle all dependencies:

**Linux (Ubuntu):**
```bash
./scripts/install-deps-linux.sh
```

**macOS:**
```bash
./scripts/install-deps-macos.sh
```

Then create `~/.agent-interface.toml` (see [Configuration](#6-configuration) for both backend examples).

Build and run:
```bash
go install .
agent-interface
```


## Manual Setup

### 1. System dependencies

<details>
<summary>Linux (Ubuntu)</summary>

```bash
sudo apt update
sudo apt install -y gcc g++ make libgl1-mesa-dev xorg-dev libwayland-dev libxkbcommon-dev alsa-utils sox
```
</details>

<details>
<summary>macOS</summary>

```bash
xcode-select --install   # if not already installed
brew install sox go pipx
```

macOS will prompt for microphone permission on first use -- grant it when asked.
</details>

### 2. Install Go

<details>
<summary>Linux</summary>

```bash
curl -LO https://go.dev/dl/go1.26.2.linux-amd64.tar.gz
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf go1.26.2.linux-amd64.tar.gz
```

Add to `~/.bashrc`:
```
export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin
```
</details>

<details>
<summary>macOS</summary>

```bash
brew install go
```

Or download from https://go.dev/dl/
</details>

### 3. Install Piper TTS

```bash
pipx install piper-tts
```

Download a voice:
```bash
mkdir -p ~/.local/share/piper/voices
cd ~/.local/share/piper/voices
curl -LO https://huggingface.co/rhasspy/piper-voices/resolve/main/en/en_US/ryan/medium/en_US-ryan-medium.onnx
curl -LO https://huggingface.co/rhasspy/piper-voices/resolve/main/en/en_US/ryan/medium/en_US-ryan-medium.onnx.json
```

Browse more voices: https://rhasspy.github.io/piper-samples/

### 4. Install Bucky (whisper.cpp)

```bash
go install github.com/ardanlabs/bucky@latest

mkdir -p ~/.local/share/agent-interface/lib
bucky install -lib ~/.local/share/agent-interface/lib

bucky model get base.en
mkdir -p ~/.local/share/agent-interface/models
mv ~/models/ggml-base.en.bin ~/.local/share/agent-interface/models/
```

### 5. Global hotkey (optional)

The in-app hotkey (Ctrl+Space) works when the window is focused on both platforms. For a system-wide hotkey that works from any window:

<details>
<summary>Linux (keyd)</summary>

```bash
sudo apt install keyd
sudo systemctl enable --now keyd
```

Create `/etc/keyd/default.conf`:
```
[ids]
*

[main]

[control]
space = command(pkill -USR1 -f agent-interface)
```

```bash
sudo keyd reload
```
</details>

<details>
<summary>macOS (skhd)</summary>

```bash
brew install koekeishiya/formulae/skhd
skhd --start-service
```

Add to `~/.skhdrc`:
```
ctrl - space : pkill -USR1 -f agent-interface
```

Grant Accessibility permission to skhd when prompted.

Alternatively, trigger manually from any terminal:
```bash
pkill -USR1 -f agent-interface
```
</details>

### 6. Configuration

Create `~/.agent-interface.toml`. The `backend` field selects which LLM to use.

**Brave Search (default):**
```toml
backend        = "brave"
brave_api_key  = "YOUR_BRAVE_API_KEY"
bucky_lib      = "/path/to/.local/share/agent-interface/lib"
whisper_model  = "/path/to/.local/share/agent-interface/models/ggml-base.en.bin"
piper_model    = "/path/to/.local/share/piper/voices/en_US-ryan-medium.onnx"
```

**Claude via Bedrock proxy:**
```toml
backend            = "claude"
claude_base_url    = "https://your-bedrock-proxy.example.com/bedrock"
claude_model       = "us.anthropic.claude-sonnet-4-6"
claude_api_key     = "-"
claude_use_bedrock = true
system_prompt      = "You are a helpful voice assistant. Keep responses concise and conversational."
bucky_lib          = "/path/to/.local/share/agent-interface/lib"
whisper_model      = "/path/to/.local/share/agent-interface/models/ggml-base.en.bin"
piper_model        = "/path/to/.local/share/piper/voices/en_US-ryan-medium.onnx"
```

**Claude via Bedrock directly (no proxy):**
```toml
backend            = "claude"
claude_model       = "us.anthropic.claude-sonnet-4-6"
claude_use_bedrock = true
system_prompt      = "You are a helpful voice assistant. Keep responses concise and conversational."
bucky_lib          = "/path/to/.local/share/agent-interface/lib"
whisper_model      = "/path/to/.local/share/agent-interface/models/ggml-base.en.bin"
piper_model        = "/path/to/.local/share/piper/voices/en_US-ryan-medium.onnx"
```

When `claude_base_url` is omitted, requests go directly to `bedrock-runtime.us-east-1.amazonaws.com`. AWS credentials are resolved from the standard chain (`~/.aws/credentials`, environment variables, IAM role, etc.).

When `claude_base_url` is set, requests are routed through the proxy. Set `claude_api_key = "-"` if the proxy handles authentication.

| Field              | Backend | Description                                                                 |
| ------------------ | ------- | --------------------------------------------------------------------------- |
| `backend`          | --      | `"brave"` (default) or `"claude"`                                           |
| `brave_api_key`    | brave   | Brave Search API key                                                        |
| `claude_base_url`  | claude  | Bedrock proxy URL (omit to hit Bedrock directly)                            |
| `claude_model`     | claude  | Model ID (default: `us.anthropic.claude-sonnet-4-6`)                        |
| `claude_api_key`   | claude  | API key (`"-"` if auth is handled by the proxy)                             |
| `claude_use_bedrock` | claude | `true` for Bedrock (proxy or direct), `false` for Anthropic API directly  |
| `system_prompt`    | claude  | System prompt (default: helpful voice assistant)                            |
| `bucky_lib`        | --      | Path to whisper.cpp shared libs                                             |
| `whisper_model`    | --      | Path to whisper model (.bin)                                                |
| `piper_model`      | --      | Path to Piper voice (.onnx)                                                |

### 7. Build and run

```bash
cd agent-interface
go get ./...
go install .
agent-interface
```

First build takes ~2-5 minutes (GLFW C compilation). Subsequent builds: ~3-10s.


## Usage

| Action                 | How                                                              |
| ---------------------- | ---------------------------------------------------------------- |
| Talk                   | Press Ctrl+Space (global, works from any window) or click Talk   |
| Stop speaking          | Click Stop                                                       |
| Type instead           | Use the text input + Send                                        |
| Toggle voice response  | Voice response checkbox                                          |
| Change hotkey          | Configure button                                                 |
| Copy conversation text | Click-drag to select, Ctrl+C (Cmd+C on macOS)                   |

Beeps: 800 Hz = recording started, 400 Hz = recording stopped.


## Architecture

```
Mic -> rec/arecord (silence-detected raw PCM S16_LE)
    -> S16_LE -> float32 conversion
    -> whisper.cpp (bucky) transcription
    -> LLM backend (Brave Search or Claude via Bedrock)
    -> cleanForSpeech (strip markdown, usage JSON)
    -> Piper TTS (raw PCM 22050 Hz)
    -> play/aplay
```

Audio commands are platform-specific:
- **Linux:** `arecord` / `aplay` (ALSA)
- **macOS:** `rec` / `play` (SoX)

LLM backends:
- **Brave Search:** Single-turn, OpenAI-compatible streaming API. Context is passed as a text prefix.
- **Claude (Bedrock):** Multi-turn Messages API with proper conversation history. Supports system prompts.

Global hotkey path:
```
Ctrl+Space -> keyd/skhd -> pkill -USR1 -> Go signal handler -> triggerTalk()
```


## Troubleshooting

| Symptom                              | Platform | Fix                                                                                 |
| ------------------------------------ | -------- | ------------------------------------------------------------------------------------ |
| wayland-client-core.h: No such file  | Linux    | `sudo apt install libwayland-dev libxkbcommon-dev`                                   |
| piper: Unable to find voice          | Both     | Download the .onnx + .onnx.json to the path in your config                          |
| Fyne DoAndWait errors                | Both     | All widget calls from goroutines must be wrapped in `fyne.DoAndWait`                 |
| rec/arecord eats your keystrokes     | Both     | `cmd.Stdin = nil` on the record exec (already handled)                               |
| Piper sounds robotic                 | Both     | Try a different voice (see piper-samples page)                                       |
| Hotkey not firing                    | Linux    | `keyd monitor` to verify keyd sees the keypress; check `sudo systemctl status keyd`  |
| Hotkey not firing                    | macOS    | Check skhd is running (`brew services list`), verify Accessibility permission        |
| play silent                          | Linux    | `sudo apt install libsox-fmt-all`                                                   |
| GNOME eating Ctrl+Space              | Linux    | `gsettings set org.gnome.desktop.wm.keybindings switch-input-source "[]"`            |
| Microphone not working               | macOS    | Grant microphone permission in System Settings > Privacy & Security > Microphone     |
| No sound output                      | macOS    | Check `sox` is installed: `brew install sox`                                         |
| Claude: connection refused            | Both     | Verify `claude_base_url` is reachable and the proxy is running                       |
| Claude: 401 unauthorized              | Both     | Check `claude_api_key` or proxy auth configuration                                   |


## Notes

- **Brave backend:** single-turn only (1 user message per API call). Conversation context is passed as a text prefix, which grows each turn.
- **Claude backend:** true multi-turn conversation with proper message history. Supports system prompts. The Clear button resets the conversation.
- `base.en` whisper model is ~400 ms per 5s clip on CPU. Use `small.en` for better accuracy at ~1s latency.
- Piper medium voices are ~200-400 ms per sentence on CPU. High voices are ~1s+.
- Total round-trip latency: ~5s (recording) + ~0.5s (STT) + ~1-3s (API) + ~0.5-1s (TTS) = ~7-10s.
- The app traps SIGUSR1 for the global hotkey. Any `kill -USR1 $(pidof agent-interface)` triggers a talk cycle.
