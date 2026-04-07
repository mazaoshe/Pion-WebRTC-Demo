package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"

	"github.com/pion/webrtc/v4"
	"gopkg.in/hraban/opus.v2"
)

const (
	senseVoiceURL = "http://localhost:8765/transcribe"
	sampleRate    = 48000
	channels      = 1
	chunkDuration = 5 * time.Second
	targetRate    = 16000
	maxFrameSize  = 5760 // 48kHz * 120ms
)

type TranscribeResult struct {
	Text string `json:"text"`
}

// 启动音频识别流水线
func startSenseVoicePipeline(localID string, track *webrtc.TrackRemote) {
	dec, err := opus.NewDecoder(sampleRate, channels)
	if err != nil {
		fmt.Printf("[SenseVoice] 创建 Opus 解码器失败: %v\n", err)
		return
	}

	var pcmBuf []int16
	ticker := time.NewTicker(chunkDuration)
	defer ticker.Stop()

	rtpBuf := make([]byte, 1500)
	pcmFrame := make([]int16, maxFrameSize)

	for {
		// 非阻塞地检查是否到了发送时间
		select {
		case <-ticker.C:
			if len(pcmBuf) > 0 {
				chunk := make([]int16, len(pcmBuf))
				copy(chunk, pcmBuf)
				pcmBuf = pcmBuf[:0]
				go sendAndBroadcast(localID, chunk)
			}
		default:
		}

		// 读取 RTP 包（带超时避免永久阻塞）
		track.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		n, _, err := track.Read(rtpBuf)
		if err != nil {
			// 超时是正常的，继续
			continue
		}

		// Opus 解码 → PCM int16，48kHz
		n, err = dec.Decode(rtpBuf[:n], pcmFrame)
		if err != nil {
			continue
		}

		// 降采样 48kHz → 16kHz（每3个取1个）
		for i := 0; i < n; i += 3 {
			pcmBuf = append(pcmBuf, pcmFrame[i])
		}
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
	if err := json.Unmarshal(raw, &result); err != nil || result.Text == "" {
		return
	}

	fmt.Printf("[SenseVoice] %s 识别结果: %s\n", localID, result.Text)

	// 构造消息，通过 DataChannel 发给同频道所有人
	msg := fmt.Sprintf(
		`{"type":"transcribe","from":"%s","text":"%s"}`,
		localID, result.Text,
	)

	mutex.Lock()
	defer mutex.Unlock()
	for id, dc := range dataChannels {
		if sameChannel(id, localID) { // 包括自己，方便前端显示
			dc.SendText(msg)
		}
	}
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
