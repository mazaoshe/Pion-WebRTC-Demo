package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
)

type VideoSenderInfo struct {
	pc   *webrtc.PeerConnection
	ssrc webrtc.SSRC
}

var (
	// upgrader 用于将 HTTP 连接升级为 WebSocket 连接
	upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			// 允许所有来源的连接，开发环境常用
			return true
		},
	}

	// dataChannels 存储所有活跃的数据通道，用于消息广播
	dataChannels = make(map[string]*webrtc.DataChannel)

	// audioTracks 存储所有活跃的下行音频轨道
	audioTracks = make(map[string]*webrtc.TrackLocalStaticRTP)

	// videoTracks 存储所有活跃的下行视频轨道
	videoTracks = make(map[string]*webrtc.TrackLocalStaticRTP)

	videoSenders = make(map[string]*VideoSenderInfo)

	// mutex 互斥锁，保证对 map 的并发访问安全
	mutex = &sync.Mutex{}

	// channelID 用于生成唯一的 peer 标识符
	channelID = 0

	// 预设的视频比特率 (可根据需要调整)
	VideoBitrate = 500 * 1000 // 500 kbps

	// 每个 peer 属于哪个频道
	peerChannels = make(map[string]string)
)

func main() {
	// 静态文件服务
	fs := http.FileServer(http.Dir("./static"))
	http.Handle("/static/", http.StripPrefix("/static/", fs))

	// 处理 WebSocket 请求
	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		channel := strings.TrimSpace(r.URL.Query().Get("channel"))
		if channel == "" {
			channel = "默认频道"
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Print("upgrade failed: ", err)
			return
		}
		defer conn.Close()

		// 准备媒体引擎 (必须，用于处理音频)
		m := &webrtc.MediaEngine{}
		if err := m.RegisterDefaultCodecs(); err != nil {
			log.Print("RegisterDefaultCodecs failed: ", err)
			return
		}

		iceURLs := parseICEURLs(os.Getenv("STUN_URLS"))
		configuration := webrtc.Configuration{}
		if len(iceURLs) > 0 {
			configuration.ICEServers = []webrtc.ICEServer{{URLs: iceURLs}}
		}

		se := webrtc.SettingEngine{}
		if min, max, ok := parseUDPRange(os.Getenv("UDP_MIN"), os.Getenv("UDP_MAX")); ok {
			se.SetEphemeralUDPPortRange(min, max)
		}
		if publicIP := strings.TrimSpace(os.Getenv("PUBLIC_IP")); publicIP != "" {
			se.SetNAT1To1IPs([]string{publicIP}, webrtc.ICECandidateTypeHost)
		}

		// 创建 API 对象
		api := webrtc.NewAPI(
			webrtc.WithMediaEngine(m),
			webrtc.WithSettingEngine(se),
		)

		// 使用自定义的 API 对象创建 PeerConnection
		peerConnection, err := api.NewPeerConnection(configuration)
		if err != nil {
			log.Print("NewPeerConnection failed: ", err)
			return
		}
		defer peerConnection.Close()

		// 为当前连接生成唯一 ID
		mutex.Lock()
		channelID++
		localID := fmt.Sprintf("peer-%d", channelID)
		peerChannels[localID] = channel
		mutex.Unlock()

		// --- 音视频处理核心逻辑 (SFU) ---

		// 1. 创建一个用于向此客户端发送音频的本地轨道 (Downlink)
		// 我们假设使用 Opus 编码
		outputTrack, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus}, "audio", "pion")
		if err != nil {
			log.Print("创建本地音频轨道失败: ", err)
			return
		}

		// 将这个轨道添加到 PeerConnection，这样它就会包含在发给客户端的 Answer 中
		rtpSender, err := peerConnection.AddTrack(outputTrack)
		if err != nil {
			log.Print("AddTrack failed: ", err)
			return
		}

		// 读取 RTCP 包 (保持连接活跃需要)
		go func() {
			rtcpBuf := make([]byte, 1500)
			for {
				if _, _, rtcpErr := rtpSender.Read(rtcpBuf); rtcpErr != nil {
					return
				}
			}
		}()

		outputVideoTrack, err := webrtc.NewTrackLocalStaticRTP(
			webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8, ClockRate: 90000, SDPFmtpLine: fmt.Sprintf("x-google-start-bitrate=%d;x-google-max-bitrate=%d", VideoBitrate/1000, VideoBitrate/1000)},
			"video",
			"pion",
		)
		if err != nil {
			log.Print("创建本地视频轨道失败: ", err)
			return
		}

		// 将视频轨道添加到 PeerConnection
		videoSender, err := peerConnection.AddTrack(outputVideoTrack)
		if err != nil {
			log.Print("AddTrack video failed: ", err)
			return
		}
		// 读取视频 RTCP 包（新增）
		go func() {
			for {
				pkts, _, rtcpErr := videoSender.ReadRTCP()
				if rtcpErr != nil {
					return
				}
				for _, pkt := range pkts {
					switch p := pkt.(type) {
					case *rtcp.PictureLossIndication, *rtcp.FullIntraRequest:
						mutex.Lock()
						for id, info := range videoSenders {
							if id != localID && sameChannel(id, localID) {
								info.pc.WriteRTCP([]rtcp.Packet{
									&rtcp.PictureLossIndication{
										MediaSSRC: uint32(info.ssrc),
									},
								})
							}
						}
						mutex.Unlock()
					case *rtcp.ReceiverEstimatedMaximumBitrate:
						mutex.Lock()
						for id, info := range videoSenders {
							if id != localID && sameChannel(id, localID) {
								info.pc.WriteRTCP([]rtcp.Packet{
									&rtcp.ReceiverEstimatedMaximumBitrate{
										Bitrate: p.Bitrate,
										SSRCs:   []uint32{uint32(info.ssrc)},
									},
								})
							}
						}
						mutex.Unlock()
					}
				}
			}
		}()

		// 将此下行轨道注册到全局 Map 中
		mutex.Lock()
		audioTracks[localID] = outputTrack
		videoTracks[localID] = outputVideoTrack
		mutex.Unlock()

		// 2. 监听来自此客户端的音频流 (Uplink)
		peerConnection.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
			log.Printf("收到来自 '%s' 的音频轨道: %s \n", localID, track.Codec().MimeType)

			if track.Kind() == webrtc.RTPCodecTypeAudio {

				// 循环读取 RTP 音频包
				for {
					rtpPacket, _, readErr := track.ReadRTP()
					if readErr != nil {
						log.Printf("读取 RTP 包失败或连接断开: %v", readErr)
						return
					}

					// 将收到的音频包广播给其他所有客户端的下行轨道
					mutex.Lock()
					for id, outTrack := range audioTracks {
						// 不发给自己，防止回音
						if id != localID && sameChannel(id, localID) {
							if writeErr := outTrack.WriteRTP(rtpPacket); writeErr != nil {
								// 忽略由于通道刚刚关闭导致的写入错误
							}
						}
					}
					mutex.Unlock()
				}
			} else if track.Kind() == webrtc.RTPCodecTypeVideo {

				mutex.Lock()
				videoSenders[localID] = &VideoSenderInfo{
					pc:   peerConnection,
					ssrc: track.SSRC(),
				}
				mutex.Unlock()

				go func() {
					mutex.Lock()
					for id, info := range videoSenders {
						if id != localID && sameChannel(id, localID) {
							info.pc.WriteRTCP([]rtcp.Packet{
								&rtcp.PictureLossIndication{
									MediaSSRC: uint32(info.ssrc),
								},
							})
						}
					}
					mutex.Unlock()
				}()

				for {
					rtpPacket, _, readErr := track.ReadRTP()
					if readErr != nil {
						return
					}
					mutex.Lock()
					for id, outTrack := range videoTracks {
						if id != localID && sameChannel(id, localID) {
							outTrack.WriteRTP(rtpPacket)
						}
					}
					mutex.Unlock()
				}
			}
		})

		// --- 数据通道处理 ---
		peerConnection.OnDataChannel(func(d *webrtc.DataChannel) {
			log.Printf("收到新的数据通道: %s %d\n", d.Label(), d.ID())

			d.OnOpen(func() {
				log.Printf("数据通道 '%s' 已打开\n", localID)
				mutex.Lock()
				dataChannels[localID] = d
				mutex.Unlock()
			})

			d.OnMessage(func(msg webrtc.DataChannelMessage) {
				mutex.Lock()
				defer mutex.Unlock()
				for id, dc := range dataChannels {
					if id != localID && sameChannel(id, localID) {
						if err := dc.SendText(string(msg.Data)); err != nil {
						}
					}
				}
			})

			d.OnClose(func() {
				log.Printf("数据通道 '%s' 已关闭\n", localID)
				mutex.Lock()
				delete(dataChannels, localID)
				mutex.Unlock()
			})
		})

		// 清理工作
		peerConnection.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
			if s == webrtc.PeerConnectionStateClosed || s == webrtc.PeerConnectionStateFailed {
				log.Printf("PeerConnection 状态变更为 %s, 清理资源: %s\n", s.String(), localID)
				mutex.Lock()
				delete(audioTracks, localID)
				delete(videoTracks, localID)
				delete(dataChannels, localID)
				delete(videoSenders, localID)
				delete(peerChannels, localID)
				mutex.Unlock()
			}
		})

		// 4. ICE Candidate 发现
		peerConnection.OnICECandidate(func(c *webrtc.ICECandidate) {
			if c == nil {
				return
			}

			payload, err := json.Marshal(c.ToJSON())
			if err != nil {
				log.Println("ICE Candidate 序列化失败:", err)
				return
			}

			if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
				log.Println("发送 ICE Candidate 失败:", err)
			}
		})

		// 5. 信令循环：处理来自 WebSocket 的 Offer 和 ICE Candidates
		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				log.Println("读取 WebSocket 消息失败:", err)
				break
			}

			var data map[string]interface{}
			if err := json.Unmarshal(message, &data); err != nil {
				log.Print("JSON 解析失败: ", err)
				continue
			}

			// 如果收到的是 SDP (Offer)
			if _, ok := data["sdp"]; ok {
				var offer webrtc.SessionDescription
				if err := json.Unmarshal(message, &offer); err != nil {
					log.Print("Offer 解析失败: ", err)
					continue
				}

				// 设置远程描述 (Remote Description)
				if err := peerConnection.SetRemoteDescription(offer); err != nil {
					log.Print("设置远程描述失败: ", err)
					continue
				}

				// 创建应答 (Answer)
				answer, err := peerConnection.CreateAnswer(nil)
				if err != nil {
					log.Print("创建 Answer 失败: ", err)
					continue
				}

				// 设置本地描述 (Local Description)
				if err := peerConnection.SetLocalDescription(answer); err != nil {
					log.Print("设置本地描述失败: ", err)
					continue
				}

				// 将 Answer 通过 WebSocket 发回浏览器
				payload, err := json.Marshal(answer)
				if err != nil {
					log.Print("Answer 序列化失败: ", err)
					continue
				}

				if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
					log.Println("发送 Answer 失败:", err)
					break
				}
			} else if _, ok := data["candidate"]; ok {
				// 如果收到的是 ICE Candidate
				var candidate webrtc.ICECandidateInit
				if err := json.Unmarshal(message, &candidate); err != nil {
					log.Print("Candidate 解析失败: ", err)
					continue
				}

				// 将来自浏览器的候选路径添加到连接中
				if err := peerConnection.AddICECandidate(candidate); err != nil {
					log.Print("添加 ICE Candidate 失败: ", err)
					continue
				}
			}
		}
	})

	// 首页路由
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "static/index.html")
	})

	port := os.Getenv("PORT")
	if strings.TrimSpace(port) == "" {
		port = "8080"
	}

	useTLS := strings.EqualFold(strings.TrimSpace(os.Getenv("USE_TLS")), "1") || strings.EqualFold(strings.TrimSpace(os.Getenv("USE_TLS")), "true")
	certFile := os.Getenv("TLS_CERT_FILE")
	if strings.TrimSpace(certFile) == "" {
		certFile = "cert.pem"
	}
	keyFile := os.Getenv("TLS_KEY_FILE")
	if strings.TrimSpace(keyFile) == "" {
		keyFile = "key.pem"
	}

	addr := ":" + port
	if useTLS {
		log.Printf("HTTPS 服务器启动，监听端口 %s...\n", addr)
		err := http.ListenAndServeTLS(addr, certFile, keyFile, nil)
		if err != nil {
			log.Fatal("服务器启动失败: ", err)
		}
		return
	}

	log.Printf("HTTP 服务器启动，监听端口 %s...\n", addr)
	err := http.ListenAndServe(addr, nil)
	if err != nil {
		log.Fatal("服务器启动失败: ", err)
	}
}

func parseICEURLs(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []string{"stun:stun-test.meilebei.com:3478"}
	}

	parts := strings.Split(raw, ",")
	urls := make([]string, 0, len(parts))
	for _, p := range parts {
		u := strings.TrimSpace(p)
		if u == "" {
			continue
		}
		urls = append(urls, u)
	}

	if len(urls) == 0 {
		return []string{"stun:stun-test.meilebei.com:3478"}
	}

	return urls
}

func parseUDPRange(minRaw, maxRaw string) (uint16, uint16, bool) {
	minRaw = strings.TrimSpace(minRaw)
	maxRaw = strings.TrimSpace(maxRaw)
	if minRaw == "" || maxRaw == "" {
		return 0, 0, false
	}

	minInt, err := strconv.Atoi(minRaw)
	if err != nil {
		return 0, 0, false
	}
	maxInt, err := strconv.Atoi(maxRaw)
	if err != nil {
		return 0, 0, false
	}
	if minInt <= 0 || maxInt <= 0 || minInt >= maxInt {
		return 0, 0, false
	}
	if minInt > 65535 || maxInt > 65535 {
		return 0, 0, false
	}
	return uint16(minInt), uint16(maxInt), true
}

// 判断两个用户是否同组
func sameChannel(a, b string) bool {
	return peerChannels[a] == peerChannels[b]
}
