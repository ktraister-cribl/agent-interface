package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
	"fyne.io/fyne/v2/theme"
	"image/color"

	"github.com/ardanlabs/bucky/pkg/whisper"
	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

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
)

func main() {
	buckyLib = os.Getenv("BUCKY_LIB")
	whisperModel = os.Getenv("BUCKY_TEST_MODEL")
	piperModel = os.Getenv("PIPER_TEST_MODEL")
	braveKey = os.Getenv("BRAVE_API_KEY")

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

	    go func() {
		wavData := recordAudio()
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

	// Stop button:
	stopBtn := widget.NewButton("Stop", func() {
		aplayMu.Lock()
		if aplayProc != nil && atomic.LoadInt32(&aplayRunning) == 1 {
			aplayProc.Process.Kill()
		}
		aplayMu.Unlock()
		status.SetText("Ready")
	})

	// Configure button
	configureBtn := widget.NewButton("Configure", func() {
		options := []string{
			"Ctrl+Space",
			"Ctrl+T",
			"F9",
			"F10",
			"Ctrl+Alt+Space",
		}

		keyMap := map[string]keyBinding{
			"Ctrl+Space":     {fyne.KeySpace, fyne.KeyModifierControl},
			"Ctrl+T":         {fyne.KeyT, fyne.KeyModifierControl},
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
	buttonRow := container.NewHBox(talkBtn, stopBtn, configureBtn, voiceCheck)
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

	w.ShowAndRun()
}

// --- Audio recording (sox + arecord, silence detection) ---
func recordAudio() []byte {
	tmpFile, err := os.CreateTemp("", "rec_*.raw")
	if err != nil {
		return nil
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)
	tmpFile.Close()

	// Fixed 5-second recording, raw PCM (no WAV header to corrupt)
	cmd := exec.Command("arecord", "-f", "S16_LE", "-r", "16000", "-c", "1", "-d", "5", tmpPath)
	cmd.Stdin = nil
	if err := cmd.Run(); err != nil {
		return nil
	}

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
	fyne.DoAndWait(func() {
		appendMessage(history, "You: "+query)
		status.SetText("Thinking...")
	})

	stream := client.Chat.Completions.NewStreaming(context.Background(), openai.ChatCompletionNewParams{
		Model: openai.ChatModel("brave"),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage(query),
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
