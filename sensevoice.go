package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"gopkg.in/hraban/opus.v2"
)

const (
	senseVoiceURL = "http://175.27.225.87:8765/transcribe"
	sampleRate    = 48000
	channels      = 1
	targetRate    = 16000
	maxFrameSize  = 5760 // 48kHz * 120ms
)

type TranscribeResult struct {
	Text string `json:"text"`
}

type SenseVoicePipeline struct {
	localID string
	dec     *opus.Decoder

	mu         sync.Mutex
	active     bool
	flushTimer *time.Timer

	pcm48k  []int16
	segment []int16

	segments chan []int16
	wg       sync.WaitGroup
}

func newSenseVoicePipeline(localID string) (*SenseVoicePipeline, error) {
	dec, err := opus.NewDecoder(sampleRate, channels)
	if err != nil {
		return nil, err
	}

	p := &SenseVoicePipeline{
		localID:  localID,
		dec:      dec,
		pcm48k:   make([]int16, maxFrameSize),
		segments: make(chan []int16, 8),
	}

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		for seg := range p.segments {
			sendAndBroadcast(p.localID, seg)
		}
	}()

	return p, nil
}

func (p *SenseVoicePipeline) Close() {
	p.FlushNow()
	close(p.segments)
	p.wg.Wait()
}

func (p *SenseVoicePipeline) SetActive(active bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if active {
		p.active = true
		p.segment = p.segment[:0]
		if p.flushTimer != nil {
			p.flushTimer.Stop()
			p.flushTimer = nil
		}
		return
	}

	p.active = false
	if p.flushTimer != nil {
		p.flushTimer.Stop()
		p.flushTimer = nil
	}
	p.flushTimer = time.AfterFunc(250*time.Millisecond, p.FlushNow)
}

func (p *SenseVoicePipeline) PushOpus(payload []byte) {
	if len(payload) == 0 {
		return
	}

	n, err := p.dec.Decode(payload, p.pcm48k)
	if err != nil || n <= 0 {
		return
	}

	pcm16k := downsample48kTo16k(p.pcm48k[:n])
	if len(pcm16k) == 0 {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.active {
		p.segment = append(p.segment, pcm16k...)
	}
}

func downsample48kTo16k(pcm48k []int16) []int16 {
	if len(pcm48k) < 3 {
		return nil
	}
	out := make([]int16, 0, len(pcm48k)/3)
	for i := 0; i+2 < len(pcm48k); i += 3 {
		v := int32(pcm48k[i]) + int32(pcm48k[i+1]) + int32(pcm48k[i+2])
		out = append(out, int16(v/3))
	}
	return out
}

func minSegmentSamples() int {
	return targetRate * 3 / 20
}

func (p *SenseVoicePipeline) FlushNow() {
	var seg []int16

	p.mu.Lock()
	if p.flushTimer != nil {
		p.flushTimer.Stop()
		p.flushTimer = nil
	}
	if p.active || len(p.segment) < minSegmentSamples() {
		p.segment = p.segment[:0]
		p.mu.Unlock()
		return
	}
	seg = make([]int16, len(p.segment))
	copy(seg, p.segment)
	p.segment = p.segment[:0]
	p.mu.Unlock()

	select {
	case p.segments <- seg:
	default:
	}
}

// 发送到 SenseVoice 并通过 DataChannel 广播结果
func sendAndBroadcast(localID string, pcm []int16) {
	wavData := pcmToWav(pcm, targetRate, channels)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "audio.wav")
	if err != nil {
		return
	}
	part.Write(wavData)
	writer.Close()

	resp, err := http.Post(senseVoiceURL, writer.FormDataContentType(), &body)
	if err != nil {
		fmt.Printf("[SenseVoice] 请求失败: %v\n", err)
		return
	}
	defer resp.Body.Close()

	// 如果服务忙（429）直接跳过
	if resp.StatusCode == 429 {
		fmt.Printf("[SenseVoice] 服务忙，跳过本段\n")
		return
	}

	raw, _ := io.ReadAll(resp.Body)
	var result TranscribeResult
	if err := json.Unmarshal(raw, &result); err != nil || strings.TrimSpace(result.Text) == "" {
		return
	}

	text := cleanSenseVoiceText(result.Text)
	if text == "" {
		return
	}

	nick := ""
	mutex.Lock()
	nick = peerNicknames[localID]
	mutex.Unlock()
	if strings.TrimSpace(nick) == "" {
		nick = localID
	}

	fmt.Printf("[SenseVoice] %s 识别结果: %s\n", nick, text)

	// 构造消息，通过 DataChannel 发给同频道所有人
	msgBytes, err := json.Marshal(struct {
		Type string `json:"type"`
		From string `json:"from"`
		Nick string `json:"nick"`
		Text string `json:"text"`
	}{
		Type: "transcribe",
		From: localID,
		Nick: nick,
		Text: text,
	})
	if err != nil {
		return
	}

	mutex.Lock()
	defer mutex.Unlock()
	for id, dc := range dataChannels {
		if sameChannel(id, localID) { // 包括自己，方便前端显示
			dc.SendText(string(msgBytes))
		}
	}
}

var senseVoiceTokenRE = regexp.MustCompile(`<\|[^>]+\|>`)

func cleanSenseVoiceText(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	s = senseVoiceTokenRE.ReplaceAllString(s, " ")
	s = strings.ReplaceAll(s, "\u0000", "")
	s = strings.Join(strings.Fields(s), " ")
	return strings.TrimSpace(s)
}

// PCM int16 → WAV bytes
func pcmToWav(pcm []int16, sampleRate, channels int) []byte {
	var buf bytes.Buffer

	dataSize := len(pcm) * 2 // int16 = 2 bytes
	byteRate := sampleRate * channels * 2
	blockAlign := channels * 2

	// WAV Header
	buf.WriteString("RIFF")
	binary.Write(&buf, binary.LittleEndian, uint32(36+dataSize))
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	binary.Write(&buf, binary.LittleEndian, uint32(16)) // chunk size
	binary.Write(&buf, binary.LittleEndian, uint16(1))  // PCM
	binary.Write(&buf, binary.LittleEndian, uint16(channels))
	binary.Write(&buf, binary.LittleEndian, uint32(sampleRate))
	binary.Write(&buf, binary.LittleEndian, uint32(byteRate))
	binary.Write(&buf, binary.LittleEndian, uint16(blockAlign))
	binary.Write(&buf, binary.LittleEndian, uint16(16)) // bits per sample
	buf.WriteString("data")
	binary.Write(&buf, binary.LittleEndian, uint32(dataSize))

	// PCM data
	for _, sample := range pcm {
		binary.Write(&buf, binary.LittleEndian, sample)
	}

	return buf.Bytes()
}
