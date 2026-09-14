package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"os/signal"
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
	"github.com/ardanlabs/bucky/pkg/whisper"
	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

type Config struct {
	BraveAPIKey  string `toml:"brave_api_key"`
	BuckyLib     string `toml:"bucky_lib"`
	WhisperModel string `toml:"whisper_model"`
	PiperModel   string `toml:"piper_model"`
}

type keyBinding struct {
	key fyne.KeyName
	mod fyne.KeyModifier
}

type whiteDisabledTheme struct {
	fyne.Theme
}

var (
	braveKey        string
	piperModel      string
	whisperModel    string
	buckyLib        string
	busy            int32
	wctx            whisper.Context
	client          openai.Client
	mu              sync.Mutex
	aplayProc       *exec.Cmd
	aplayMu         sync.Mutex
	aplayRunning    int32 // use atomic
	currentShortcut *desktop.CustomShortcut
	conversation    strings.Builder
)

func main() {
	cfg := loadConfig()
	fmt.Fprintf(os.Stderr, "DEBUG: model=%q lib=%q piper=%q\n", cfg.WhisperModel, cfg.BuckyLib, cfg.PiperModel)

	braveKey = cfg.BraveAPIKey
	buckyLib = cfg.BuckyLib
	whisperModel = cfg.WhisperModel
	piperModel = cfg.PiperModel

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

	// Init Brave client
	client = openai.NewClient(
		option.WithAPIKey(braveKey),
		option.WithBaseURL("https://api.search.brave.com/res/v1"),
	)

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
		conversation.Reset()
	})
	clearBtn.Importance = widget.WarningImportance

	// Talk button
	var talkBtn *widget.Button

	// Inside main(), after talkBtn and status are defined:
	triggerTalk := func() {
		// Reject if already processing or speaking
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
				// BLANK_AUDIO — silently ignore
				atomic.StoreInt32(&busy, 0)
				fyne.DoAndWait(func() {
					status.SetText("Ready")
					talkBtn.Enable()
				})
				return
			}
			fyne.DoAndWait(func() { talkBtn.Enable() })

			// handleQuery + speak runs here, then clears busy
			go func() {
				handleQuery(text, history, status, voiceCheck)
				atomic.StoreInt32(&busy, 0)
				fyne.DoAndWait(func() { status.SetText("Ready") })
			}()
		}()
	}

	talkBtn = widget.NewButton("Talk", triggerTalk)
	talkBtn.Importance = widget.SuccessImportance

	// Stop button:
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
		options := []string{
			"Ctrl+Space",
			"Ctrl+T",
			"Delete",
			"F9",
			"F10",
			"Ctrl+Alt+Space",
		}

		keyMap := map[string]keyBinding{
			"Ctrl+Space":     {fyne.KeySpace, fyne.KeyModifierControl},
			"Ctrl+T":         {fyne.KeyT, fyne.KeyModifierControl},
			"Delete":         {fyne.KeyDelete, 0},
			"F9":             {fyne.KeyF9, 0},
			"F10":            {fyne.KeyF10, 0},
			"Ctrl+Alt+Space": {fyne.KeySpace, fyne.KeyModifierControl | fyne.KeyModifierAlt},
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

	// Default hotkey: Ctrl+Space
	currentShortcut = &desktop.CustomShortcut{
		KeyName:  fyne.KeySpace,
		Modifier: fyne.KeyModifierControl,
	}
	w.Canvas().AddShortcut(currentShortcut, func(_ fyne.Shortcut) {
		triggerTalk()
	})
	status.SetText("Ready (hotkey: Ctrl+Space)")

	// In main(), before w.ShowAndRun():
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

// --- Audio recording (sox + arecord, silence detection) ---
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

	cmd := exec.Command("arecord", "-f", "S16_LE", "-r", "16000", "-c", "1", "-t", "raw", "-")
	cmd.Stdin = nil
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
			tmpFile.Write(frame) // only write frames that contain speech (and surrounding context)
		} else if speechDetected {
			tmpFile.Write(frame) // write trailing silence too (natural ending)
			silenceFrames++
			if silenceFrames*frameMs >= silenceMs {
				break // done — 1s of silence after speech
			}
		} else {
			// Haven't detected speech yet
			if totalFrames >= startTimeout {
				break // timeout — no speech detected, return nothing
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

	// Convert S16_LE to float32 (whisper expects this)
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

// --- Handle a query ---
func handleQuery(query string, history *widget.Entry, status *widget.Label, voiceCheck *widget.Check) {

	if query == "[ Silence ]" {
		return
	}

	fyne.DoAndWait(func() {
		appendMessage(history, "You: "+query)
		status.SetText("Thinking...")
	})

	// Append to history
	conversation.WriteString("User: " + query + "\n")

	// Build the message with context
	contextPrefix := ""
	if conversation.Len() > 0 {
		contextPrefix = "Previous conversation:\n" + conversation.String() + "\n\nCurrent question: "
	}

	// Use contextPrefix + query as the message
	stream := client.Chat.Completions.NewStreaming(context.Background(), openai.ChatCompletionNewParams{
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
		fyne.DoAndWait(func() { status.SetText("Error: " + err.Error()) })
		return
	}

	fullAnswer := answer.String()
	conversation.WriteString("AI: " + fullAnswer + "\n")

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
	aplayProc = exec.Command("aplay", "-r", "22050", "-f", "S16_LE", "-t", "raw", "-")
	aplayProc.Stdin = &wavBuf
	if err := aplayProc.Start(); err != nil {
		aplayMu.Unlock()
		fmt.Fprintf(os.Stderr, "aplay: %v\n", err)
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
	history.CursorRow = len(strings.Split(history.Text, "\n")) - 1 // scroll to bottom
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
