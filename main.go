package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"image/color"

	"github.com/BurntSushi/toml"
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/bedrock"
	anthropicopt "github.com/anthropics/anthropic-sdk-go/option"
	"github.com/ardanlabs/bucky/pkg/whisper"
	"github.com/aws/aws-sdk-go-v2/aws"
	openai "github.com/openai/openai-go/v3"
	openaiopt "github.com/openai/openai-go/v3/option"
)

type Config struct {
	Backend         string `toml:"backend"` // "brave" or "claude"
	BraveAPIKey     string `toml:"brave_api_key"`
	ClaudeBaseURL   string `toml:"claude_base_url"`
	ClaudeModel     string `toml:"claude_model"`
	ClaudeAPIKey    string `toml:"claude_api_key"`
	ClaudeUseBedrock bool  `toml:"claude_use_bedrock"`
	SystemPrompt    string `toml:"system_prompt"`
	BuckyLib      string `toml:"bucky_lib"`
	WhisperModel  string `toml:"whisper_model"`
	PiperModel    string `toml:"piper_model"`
}

type keyBinding struct {
	key fyne.KeyName
	mod fyne.KeyModifier
}

type whiteDisabledTheme struct {
	fyne.Theme
}

var (
	appCfg          Config
	piperModel      string
	whisperModel    string
	buckyLib        string
	busy            int32
	wctx            whisper.Context
	mu              sync.Mutex
	aplayProc       *exec.Cmd
	aplayMu         sync.Mutex
	aplayRunning    int32
	currentShortcut *desktop.CustomShortcut

	braveClient openai.Client
	braveConvo  strings.Builder

	claudeClient   anthropic.Client
	claudeMessages []anthropic.MessageParam
	msgMu          sync.Mutex
)

func main() {
	appCfg = loadConfig()

	if appCfg.Backend == "" {
		appCfg.Backend = "brave"
	}
	if appCfg.ClaudeModel == "" {
		appCfg.ClaudeModel = "us.anthropic.claude-sonnet-4-6"
	}
	if appCfg.SystemPrompt == "" {
		appCfg.SystemPrompt = "You are a helpful voice assistant. Keep responses concise and conversational."
	}

	fmt.Fprintf(os.Stderr, "DEBUG: backend=%q model=%q lib=%q piper=%q\n",
		appCfg.Backend, appCfg.WhisperModel, appCfg.BuckyLib, appCfg.PiperModel)

	buckyLib = appCfg.BuckyLib
	whisperModel = appCfg.WhisperModel
	piperModel = appCfg.PiperModel

	// Init whisper
	if err := whisper.Load(buckyLib); err != nil {
		panic(err)
	}
	if err := whisper.Init(buckyLib); err != nil {
		panic(err)
	}
	cparams := whisper.ContextDefaultParams()
	var err error
	wctx, err = whisper.InitFromFileWithParams(whisperModel, cparams)
	if err != nil {
		panic(err)
	}
	defer whisper.Free(wctx)

	// Init LLM client
	switch appCfg.Backend {
	case "claude":
		var opts []anthropicopt.RequestOption
		if appCfg.ClaudeUseBedrock {
			token := appCfg.ClaudeAPIKey
			if token == "" {
				token = "-"
			}
			awsCfg := aws.Config{
				Region:                  "us-east-1",
				BearerAuthTokenProvider: bedrock.NewStaticBearerTokenProvider(token),
			}
			opts = append(opts, bedrock.WithConfig(awsCfg))
			if appCfg.ClaudeBaseURL != "" {
				parsed, err := url.Parse(appCfg.ClaudeBaseURL)
				if err != nil {
					fmt.Fprintf(os.Stderr, "invalid claude_base_url: %v\n", err)
					os.Exit(1)
				}
				transport := &proxyTransport{
					inner:      http.DefaultTransport,
					scheme:     parsed.Scheme,
					host:       parsed.Host,
					pathPrefix: strings.TrimRight(parsed.Path, "/"),
				}
				opts = append(opts, anthropicopt.WithHTTPClient(&http.Client{Transport: transport}))
			}
		} else {
			opts = append(opts, anthropicopt.WithAPIKey(appCfg.ClaudeAPIKey))
			if appCfg.ClaudeBaseURL != "" {
				opts = append(opts, anthropicopt.WithBaseURL(appCfg.ClaudeBaseURL))
			}
		}
		claudeClient = anthropic.NewClient(opts...)
	default:
		braveClient = openai.NewClient(
			openaiopt.WithAPIKey(appCfg.BraveAPIKey),
			openaiopt.WithBaseURL("https://api.search.brave.com/res/v1"),
		)
	}

	// --- Fyne app ---
	a := app.NewWithID("io.ktraister.agent")
	a.Settings().SetTheme(theme.DarkTheme())
	w := a.NewWindow("Agent Interface")
	w.Resize(fyne.NewSize(600, 500))

	// Text input
	textInput := widget.NewEntry()
	textInput.PlaceHolder = "Or type here..."

	// Conversation history
	history := widget.NewEntry()
	history.MultiLine = true
	history.Wrapping = fyne.TextWrapWord
	historyScroll := container.NewScroll(history)

	// Status label
	status := widget.NewLabel("Ready")

	// Voice mode toggle
	voiceCheck := widget.NewCheck("Voice response", func(checked bool) {})
	voiceCheck.SetChecked(true)

	// Send button
	sendBtn := widget.NewButton("Send", func() {
		q := strings.TrimSpace(textInput.Text)
		if q == "" {
			return
		}
		textInput.SetText("")
		go handleQuery(q, history, status, voiceCheck)
	})
	sendBtn.Importance = widget.HighImportance

	// Clear Button
	clearBtn := widget.NewButton("Clear", func() {
		history.SetText("")
		braveConvo.Reset()
		msgMu.Lock()
		claudeMessages = nil
		msgMu.Unlock()
	})
	clearBtn.Importance = widget.WarningImportance

	// Talk button
	var talkBtn *widget.Button

	triggerTalk := func() {
		if atomic.LoadInt32(&busy) == 1 {
			status.SetText("Busy...")
			return
		}
		atomic.StoreInt32(&busy, 1)

		talkBtn.Disable()
		status.SetText("Recording... speak now")
		beep(800)

		go func() {
			wavData := recordAudio()
			beep(400)
			if wavData == nil {
				atomic.StoreInt32(&busy, 0)
				fyne.DoAndWait(func() {
					status.SetText("Ready")
					talkBtn.Enable()
				})
				return
			}
			fyne.DoAndWait(func() { status.SetText("Transcribing...") })
			text := transcribe(wavData)
			if text == "" {
				atomic.StoreInt32(&busy, 0)
				fyne.DoAndWait(func() {
					status.SetText("Ready")
					talkBtn.Enable()
				})
				return
			}
			fyne.DoAndWait(func() { talkBtn.Enable() })

			go func() {
				handleQuery(text, history, status, voiceCheck)
				atomic.StoreInt32(&busy, 0)
			}()
		}()
	}

	talkBtn = widget.NewButton("Talk", triggerTalk)
	talkBtn.Importance = widget.SuccessImportance

	// Stop button
	stopBtn := widget.NewButton("Stop", func() {
		aplayMu.Lock()
		if aplayProc != nil && atomic.LoadInt32(&aplayRunning) == 1 {
			aplayProc.Process.Kill()
		}
		aplayMu.Unlock()
		status.SetText("Ready")
	})
	stopBtn.Importance = widget.DangerImportance

	// Configure button
	configureBtn := widget.NewButton("Configure", func() {
		var options []string
		keyMap := make(map[string]keyBinding)

		if runtime.GOOS == "darwin" {
			options = []string{
				"Cmd+Space",
				"Cmd+T",
				"Cmd+Shift+Space",
				"F9",
				"F10",
			}
			keyMap["Cmd+Space"] = keyBinding{fyne.KeySpace, fyne.KeyModifierSuper}
			keyMap["Cmd+T"] = keyBinding{fyne.KeyT, fyne.KeyModifierSuper}
			keyMap["Cmd+Shift+Space"] = keyBinding{fyne.KeySpace, fyne.KeyModifierSuper | fyne.KeyModifierShift}
			keyMap["F9"] = keyBinding{fyne.KeyF9, 0}
			keyMap["F10"] = keyBinding{fyne.KeyF10, 0}
		} else {
			options = []string{
				"Ctrl+Space",
				"Ctrl+T",
				"Delete",
				"F9",
				"F10",
				"Ctrl+Alt+Space",
			}
			keyMap["Ctrl+Space"] = keyBinding{fyne.KeySpace, fyne.KeyModifierControl}
			keyMap["Ctrl+T"] = keyBinding{fyne.KeyT, fyne.KeyModifierControl}
			keyMap["Delete"] = keyBinding{fyne.KeyDelete, 0}
			keyMap["F9"] = keyBinding{fyne.KeyF9, 0}
			keyMap["F10"] = keyBinding{fyne.KeyF10, 0}
			keyMap["Ctrl+Alt+Space"] = keyBinding{fyne.KeySpace, fyne.KeyModifierControl | fyne.KeyModifierAlt}
		}

		keySelect := widget.NewSelect(options, func(selected string) {
			if currentShortcut != nil {
				w.Canvas().RemoveShortcut(currentShortcut)
			}
			binding := keyMap[selected]
			currentShortcut = &desktop.CustomShortcut{KeyName: binding.key, Modifier: binding.mod}
			w.Canvas().AddShortcut(currentShortcut, func(_ fyne.Shortcut) {
				triggerTalk()
			})
			status.SetText("Hotkey set: " + selected)
		})

		content := container.NewVBox(
			widget.NewLabel("Choose a hotkey:"),
			keySelect,
		)

		dlg := dialog.NewCustom("Set Talk Hotkey", "Close", content, w)
		dlg.Resize(fyne.NewSize(300, 120))
		dlg.Show()
	})

	// Layout
	buttonRow := container.NewHBox(talkBtn, stopBtn, clearBtn, configureBtn, voiceCheck)
	inputRow := container.NewBorder(nil, nil, nil, sendBtn, textInput)

	w.SetContent(container.NewBorder(
		container.NewVBox(buttonRow, status), // top
		inputRow,                             // bottom
		nil, nil,                             // left, right
		historyScroll,
	))

	// Default hotkey: Cmd+Space on macOS, Ctrl+Space on Linux
	if runtime.GOOS == "darwin" {
		currentShortcut = &desktop.CustomShortcut{
			KeyName:  fyne.KeySpace,
			Modifier: fyne.KeyModifierSuper,
		}
	} else {
		currentShortcut = &desktop.CustomShortcut{
			KeyName:  fyne.KeySpace,
			Modifier: fyne.KeyModifierControl,
		}
	}
	w.Canvas().AddShortcut(currentShortcut, func(_ fyne.Shortcut) {
		triggerTalk()
	})
	if runtime.GOOS == "darwin" {
		status.SetText("Ready (hotkey: Cmd+Space)")
	} else {
		status.SetText("Ready (hotkey: Ctrl+Space)")
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGUSR1)
	go func() {
		for range sigCh {
			fyne.DoAndWait(func() {
				triggerTalk()
			})
		}
	}()

	w.ShowAndRun()
}

// --- Handle a query (dispatches to configured backend) ---
func handleQuery(query string, history *widget.Entry, status *widget.Label, voiceCheck *widget.Check) {
	if query == "[ Silence ]" {
		return
	}

	fyne.DoAndWait(func() {
		appendMessage(history, "You: "+query)
		status.SetText("Thinking...")
	})

	var fullAnswer string
	var err error

	switch appCfg.Backend {
	case "claude":
		fullAnswer, err = queryBedrock(query)
	default:
		fullAnswer, err = queryBrave(query)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "LLM error: %v\n", err)
		fyne.DoAndWait(func() { status.SetText("Error: " + err.Error()) })
		return
	}

	fyne.DoAndWait(func() {
		appendMessage(history, "AI: "+cleanForSpeech(fullAnswer))
	})

	if voiceCheck.Checked {
		fyne.DoAndWait(func() { status.SetText("Speaking...") })
		speak(fullAnswer)
		fyne.DoAndWait(func() { status.SetText("Ready") })
	} else {
		fyne.DoAndWait(func() { status.SetText("Ready") })
	}
}

func queryBrave(query string) (string, error) {
	braveConvo.WriteString("User: " + query + "\n")

	contextPrefix := ""
	if braveConvo.Len() > 0 {
		contextPrefix = "Previous conversation:\n" + braveConvo.String() + "\n\nCurrent question: "
	}

	stream := braveClient.Chat.Completions.NewStreaming(context.Background(), openai.ChatCompletionNewParams{
		Model: openai.ChatModel("brave"),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage(contextPrefix + query),
		},
	})

	var answer strings.Builder
	for stream.Next() {
		chunk := stream.Current()
		answer.WriteString(chunk.Choices[0].Delta.Content)
	}
	if err := stream.Err(); err != nil {
		return "", err
	}

	fullAnswer := answer.String()
	braveConvo.WriteString("AI: " + fullAnswer + "\n")
	return fullAnswer, nil
}

func queryBedrock(query string) (string, error) {
	msgMu.Lock()
	claudeMessages = append(claudeMessages, anthropic.NewUserMessage(anthropic.NewTextBlock(query)))
	messages := make([]anthropic.MessageParam, len(claudeMessages))
	copy(messages, claudeMessages)
	msgMu.Unlock()

	stream := claudeClient.Messages.NewStreaming(context.Background(), anthropic.MessageNewParams{
		Model:     appCfg.ClaudeModel,
		MaxTokens: 4096,
		System: []anthropic.TextBlockParam{{
			Text: appCfg.SystemPrompt,
		}},
		Messages: messages,
	})

	accumulated := anthropic.Message{}
	for stream.Next() {
		accumulated.Accumulate(stream.Current())
	}
	if err := stream.Err(); err != nil {
		return "", err
	}

	var answer strings.Builder
	for _, block := range accumulated.Content {
		switch v := block.AsAny().(type) {
		case anthropic.TextBlock:
			answer.WriteString(v.Text)
		}
	}

	fullAnswer := answer.String()

	msgMu.Lock()
	claudeMessages = append(claudeMessages, anthropic.NewAssistantMessage(anthropic.NewTextBlock(fullAnswer)))
	msgMu.Unlock()

	return fullAnswer, nil
}

// --- Audio recording (silence detection) ---
func recordAudio() []byte {
	const (
		sampleRate     = 16000
		frameMs        = 30
		frameSamples   = sampleRate * frameMs / 1000 // 480
		speechThresh   = 0.015                       // RMS threshold (tune: raise if noisy, lower if quiet)
		silenceMs      = 1000                        // stop after 1s of silence
		maxDurationMs  = 20000                       // hard cap: 20s
		startTimeoutMs = 3000                        // give up if no speech within 3s of starting
	)

	tmpFile, err := os.CreateTemp("", "rec_*.raw")
	if err != nil {
		return nil
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	cmd := recordCmd()
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		tmpFile.Close()
		return nil
	}
	if err := cmd.Start(); err != nil {
		tmpFile.Close()
		return nil
	}

	frameBytes := frameSamples * 2 // S16 = 2 bytes per sample
	frame := make([]byte, frameBytes)

	var (
		silenceFrames  int
		totalFrames    int
		speechDetected bool
		maxFrames      = maxDurationMs / frameMs
		startTimeout   = startTimeoutMs / frameMs
	)

	for totalFrames < maxFrames {
		_, err := io.ReadFull(pipe, frame)
		if err != nil {
			break
		}
		totalFrames++

		// Compute RMS
		rms := 0.0
		for i := 0; i < frameSamples; i++ {
			s := int16(frame[i*2]) | int16(frame[i*2+1])<<8
			f := float64(s) / 32768.0
			rms += f * f
		}
		rms = math.Sqrt(rms / float64(frameSamples))

		isSpeech := rms > speechThresh

		if isSpeech {
			speechDetected = true
			silenceFrames = 0
			tmpFile.Write(frame)
		} else if speechDetected {
			tmpFile.Write(frame)
			silenceFrames++
			if silenceFrames*frameMs >= silenceMs {
				break
			}
		} else {
			if totalFrames >= startTimeout {
				break
			}
		}
	}

	cmd.Process.Kill()
	cmd.Wait()
	tmpFile.Close()

	data, err := os.ReadFile(tmpPath)
	if err != nil || len(data) < 100 {
		return nil
	}
	return data
}

// --- Whisper transcription ---
func transcribe(rawData []byte) string {
	mu.Lock()
	defer mu.Unlock()

	numSamples := len(rawData) / 2
	samples := make([]float32, numSamples)
	for i := 0; i < numSamples; i++ {
		s := int16(rawData[i*2]) | int16(rawData[i*2+1])<<8
		samples[i] = float32(s) / 32768.0
	}

	wparams := whisper.FullDefaultParams(whisper.SamplingGreedy)
	wparams.NoTimestamps = 1
	if err := whisper.Full(wctx, wparams, samples); err != nil {
		fmt.Fprintf(os.Stderr, "whisper.Full: %v\n", err)
		return ""
	}

	var sb strings.Builder
	for i := int32(0); i < whisper.FullNSegments(wctx); i++ {
		sb.WriteString(whisper.FullGetSegmentText(wctx, i))
	}
	return strings.TrimSpace(sb.String())
}

// --- TTS ---
func speak(text string) {
	text = cleanForSpeech(text)
	cmd := exec.Command("piper", "--model", piperModel, "--output-raw")
	cmd.Stdin = strings.NewReader(text)
	var wavBuf bytes.Buffer
	cmd.Stdout = &wavBuf
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "piper: %v\n", err)
		return
	}

	aplayMu.Lock()
	aplayProc = playCmd()
	aplayProc.Stdin = &wavBuf
	if err := aplayProc.Start(); err != nil {
		aplayMu.Unlock()
		fmt.Fprintf(os.Stderr, "playback: %v\n", err)
		return
	}
	atomic.StoreInt32(&aplayRunning, 1)
	aplayMu.Unlock()

	aplayProc.Wait()
	atomic.StoreInt32(&aplayRunning, 0)
}

// --- Helpers ---
func appendMessage(history *widget.Entry, text string) {
	current := history.Text
	history.SetText(current + text + "\n")
	history.CursorRow = len(strings.Split(history.Text, "\n")) - 1
}

func cleanForSpeech(text string) string {
	if i := strings.Index(text, "<usage>"); i >= 0 {
		text = text[:i]
	}
	for {
		start := strings.Index(text, "```")
		if start < 0 {
			break
		}
		end := strings.Index(text[start+3:], "```")
		if end < 0 {
			text = text[:start]
			break
		}
		text = text[:start] + text[start+3+end+3:]
	}
	text = strings.ReplaceAll(text, "`", "")
	text = strings.ReplaceAll(text, "**", "")
	text = strings.ReplaceAll(text, "*", "")
	for strings.Contains(text, "\n\n") {
		text = strings.ReplaceAll(text, "\n\n", "\n")
	}
	return strings.TrimSpace(text)
}

func (t whiteDisabledTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	if name == theme.ColorNameDisabled {
		return color.White
	}
	return t.Theme.Color(name, variant)
}

func beep(freq int) {
	exec.Command("play", "-n", "-q", "-r", "22050", "-c", "1",
		"synth", "0.1", "sine", fmt.Sprintf("%d", freq)).Run()
}

type proxyTransport struct {
	inner      http.RoundTripper
	scheme     string
	host       string
	pathPrefix string
}

func (t *proxyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r.URL.Scheme = t.scheme
	r.URL.Host = t.host
	r.URL.Path = t.pathPrefix + r.URL.Path
	r.Host = t.host
	return t.inner.RoundTrip(r)
}

func loadConfig() Config {
	var cfg Config
	path := os.ExpandEnv("$HOME/.agent-interface.toml")
	_, err := toml.DecodeFile(path, &cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v (expected at %s)\n", err, path)
		os.Exit(1)
	}
	return cfg
}
