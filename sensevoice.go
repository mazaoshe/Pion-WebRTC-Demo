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

	"gopkg.in/hraban/opus.v2"
)

const (
	senseVoiceURL = "http://localhost:8765/transcribe"
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

	pcm48k  []int16
	preRoll []int16
	segment []int16

	silenceSamples int

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
	if len(p.segment) >= minSegmentSamples() {
		seg := make([]int16, len(p.segment))
		copy(seg, p.segment)
		select {
		case p.segments <- seg:
		default:
		}
	}
	close(p.segments)
	p.wg.Wait()
}

func (p *SenseVoicePipeline) PushOpus(payload []byte) {
	if len(payload) == 0 {
		p.pushSilence(targetRate / 50)
		return
	}

	n, err := p.dec.Decode(payload, p.pcm48k)
	if err != nil || n <= 0 {
		return
	}

	pcm16k := downsample48kTo16k(p.pcm48k[:n])

	p.updatePreRoll(pcm16k)
	if isSpeechPCM(pcm16k) {
		if len(p.segment) == 0 && len(p.preRoll) > 0 {
			p.segment = append(p.segment, p.preRoll...)
		}
		p.segment = append(p.segment, pcm16k...)
		p.silenceSamples = 0
		if len(p.segment) >= maxSegmentSamples() {
			p.flush()
		}
		return
	}

	p.pushSilence(len(pcm16k))
}

func (p *SenseVoicePipeline) pushSilence(nSamples int) {
	if len(p.segment) == 0 {
		return
	}

	p.silenceSamples += nSamples
	if len(p.segment) >= minSegmentSamples() && p.silenceSamples >= endSilenceSamples() {
		p.flush()
	}
}

func (p *SenseVoicePipeline) updatePreRoll(pcm16k []int16) {
	const preRollMax = 3200
	if len(pcm16k) >= preRollMax {
		p.preRoll = append(p.preRoll[:0], pcm16k[len(pcm16k)-preRollMax:]...)
		return
	}
	need := preRollMax - len(p.preRoll)
	if need > 0 {
		if len(pcm16k) >= need {
			p.preRoll = append(p.preRoll, pcm16k[len(pcm16k)-need:]...)
			return
		}
		p.preRoll = append(p.preRoll, pcm16k...)
		return
	}
	p.preRoll = append(p.preRoll, pcm16k...)
	if len(p.preRoll) > preRollMax {
		p.preRoll = p.preRoll[len(p.preRoll)-preRollMax:]
	}
}

func (p *SenseVoicePipeline) flush() {
	if len(p.segment) == 0 {
		return
	}
	seg := make([]int16, len(p.segment))
	copy(seg, p.segment)
	p.segment = p.segment[:0]
	p.silenceSamples = 0

	select {
	case p.segments <- seg:
	default:
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

func isSpeechPCM(pcm []int16) bool {
	if len(pcm) == 0 {
		return false
	}
	var sum int64
	for _, s := range pcm {
		v := int64(s)
		sum += v * v
	}
	mean := sum / int64(len(pcm))
	return mean >= vadEnergyThreshold()
}

func vadEnergyThreshold() int64 {
	const rms = 250
	return int64(rms) * int64(rms)
}

func minSegmentSamples() int {
	return targetRate * 4 / 10
}

func endSilenceSamples() int {
	return targetRate * 8 / 10
}

func maxSegmentSamples() int {
	return targetRate * 12
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
