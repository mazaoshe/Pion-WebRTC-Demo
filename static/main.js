// 声明全局变量
let pc = null;
let ws = null;
let dc = null;
let localStream = null; // 保存本地流以便停止
let messages = document.getElementById('messages');
let remoteAudio = document.getElementById('remoteAudio');
let statusDiv = document.getElementById('status');
let pttBtn = document.getElementById('pttBtn');


let localVideo = document.getElementById('localVideo');
let remoteVideo = document.getElementById('remoteVideo');
let videoEnabled = false;
const VideoBitrate = 500; // 500 kbps

let statsInterval = null;
let totalBytesSent = 0;
let totalBytesReceived = 0;
let nickname = '用户';
let channel = 'test';

function setMicEnabled (enabled) {
    if (!localStream) {
        return;
    }
    localStream.getAudioTracks().forEach(track => {
        track.enabled = enabled;
    });
    statusDiv.textContent = enabled ? "状态: 正在说话" : "状态: 已连接 (静音)";
}

// 统一的入口函数
async function startApp () {

    var channelInput = document.getElementById('channelInput');
    var nicknameInput = document.getElementById('nameInput');

    // 新增：元素存在性检查
    if (!channelInput || !nicknameInput) {
        console.error('输入框元素未找到，请检查 HTML');
        statusDiv.textContent = "状态：错误 - 页面元素加载失败";
        return;
    }

    // 新增：安全获取值（防止 null.value 报错）
    channel = channelInput.value ? channelInput.value.trim() : '';
    nickname = nicknameInput.value ? nicknameInput.value.trim() : '';

    if (!channel) {
        alert("请输入频道名");
        channelInput.focus();
        return;
    }
    if (!nickname) {
        alert("请输入昵称");
        nickname.focus();
        return;
    }

    document.getElementById('startBtn').disabled = true;
    document.getElementById('leaveBtn').disabled = false;
    document.getElementById('videoBtn').disabled = false;

    document.getElementById('nameInput').disabled = true;
    document.getElementById('channelInput').disabled = true;

    pttBtn.disabled = true;
    statusDiv.textContent = "状态: 正在请求麦克风权限...";

    try {
        // 1. 获取本地麦克风音频流
        localStream = await navigator.mediaDevices.getUserMedia({
            audio: {
                echoCancellation: true,
                noiseSuppression: true,
                autoGainControl: true
            },
            video: false
        });
        statusDiv.textContent = "状态: 麦克风已就绪，正在连接...";

        // 2. 初始化 WebRTC
        pc = new RTCPeerConnection({
            iceServers: [
                { urls: ["stun:stun-test.meilebei.com:3478"] }
            ]
        });

        // 3. 将本地音频轨道添加到 PeerConnection 中
        localStream.getTracks().forEach(track => {
            pc.addTrack(track, localStream);
            console.log("本地音频轨道已添加");
        });

        forceVP8Codec();

        // 对讲机模式：默认静音，按住按钮才说话（避免自回声/啸叫）
        setMicEnabled(false);

        // 4. 监听远程发来的媒体轨道 (其他人的声音)
        pc.ontrack = event => {
            console.log("收到远程媒体轨道");
            if (event.track.kind === 'audio') {
                if (remoteAudio.srcObject !== event.streams[0]) {
                    remoteAudio.srcObject = event.streams[0];
                    statusDiv.textContent = "状态: 已连接并收到音频流";
                }
            } else if (event.track.kind === 'video') {
                if (remoteVideo.srcObject !== event.streams[0]) {
                    remoteVideo.srcObject = event.streams[0];
                    statusDiv.textContent = "状态：已连接并收到音视频流";
                }
            }
        };

        // 5. 依然保留文本聊天通道
        dc = pc.createDataChannel("chat");
        dc.onopen = () => {
            console.log("数据通道已成功建立！");

            // ✅ 发送自我介绍，让其他人知道你的昵称
            dc.send(JSON.stringify({
                type: 'system',
                event: 'hello',
                nick: nickname
            }));
        };
        dc.onmessage = event => {
            const data = JSON.parse(event.data);
            if (data.type === 'system') {
                // 系统通知
                const nick = data.from; // 服务端暂时用 peer-id，下面会改成昵称
                if (data.event === 'join') {
                    showSystemMessage(`👋 ${nick} 加入了频道`);
                } else if (data.event === 'leave') {
                    showSystemMessage(`🚪 ${nick} 离开了频道`);
                }
            } else {
                // 普通聊天消息
                appendMessage(`${data.nick}: ${data.text}`);
            }
        };

        // 6. ICE Candidate 处理
        pc.onicecandidate = event => {
            if (event.candidate && ws && ws.readyState === WebSocket.OPEN) {
                ws.send(JSON.stringify(event.candidate));
            }
        };

        // 7. 初始化 WebSocket 信令
        // 根据当前页面的协议动态决定使用 ws:// 还是 wss://
        let wsProtocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
        const encodedChannel = encodeURIComponent(channel);
        console.log(`正在连接 WebSocket 信令服务器，频道: ${channel}`);
        ws = new WebSocket(wsProtocol + "//" + window.location.host + "/ws?channel=" + encodedChannel);

        ws.onopen = function () {
            console.log("WebSocket 连接已建立，正在发起 Offer...");
            statsInterval = setInterval(updateStats, 2000);
            pc.createOffer().then(offer => {
                // 设置 SDP 带宽限制
                const modifiedSdp = limitVideoBandwidthInSdp(offer.sdp, VideoBitrate);

                console.log(modifiedSdp);
                pc.setLocalDescription({
                    type: offer.type,
                    sdp: modifiedSdp
                });
                ws.send(JSON.stringify({
                    type: offer.type,
                    sdp: modifiedSdp
                }));
            });
        };

        ws.onmessage = function (event) {
            let msg = JSON.parse(event.data);
            if (msg.sdp) {
                pc.setRemoteDescription(new RTCSessionDescription(msg));
            } else if (msg.candidate) {
                pc.addIceCandidate(new RTCIceCandidate(msg));
            }
        };

        // 8. 绑定按住说话按钮
        pttBtn.disabled = false;
        pttBtn.addEventListener('pointerdown', () => setMicEnabled(true));
        pttBtn.addEventListener('pointerup', () => setMicEnabled(false));
        pttBtn.addEventListener('pointercancel', () => setMicEnabled(false));
        pttBtn.addEventListener('mouseleave', () => setMicEnabled(false));

    } catch (err) {
        console.error("无法获取麦克风权限或初始化失败:", err);
        statusDiv.textContent = "状态: 错误 - " + err.message;
        document.getElementById('startBtn').disabled = false;
        document.getElementById('leaveBtn').disabled = true;
        pttBtn.disabled = true;
    }

    pc.onconnectionstatechange = () => {
        if (pc.connectionState === 'connected') {
            enforceBitrate();
        }
    };
}

// 离开频道的函数
function leaveApp () {
    console.log("正在离开频道...");

    setMicEnabled(false);
    pttBtn.disabled = true;

    // 1. 停止本地麦克风流
    if (localStream) {
        localStream.getTracks().forEach(track => track.stop());
        localStream = null;
        console.log("已停止麦克风录音");
    }

    // 2. 停止播放远程音频
    if (remoteAudio) remoteAudio.srcObject = null;
    if (remoteVideo) remoteVideo.srcObject = null;
    if (localVideo) localVideo.srcObject = null;

    // 3. 关闭数据通道
    if (dc) {
        dc.close();
        dc = null;
    }

    // 4. 关闭 WebRTC 连接
    if (pc) {
        pc.close();
        pc = null;
        console.log("已关闭 WebRTC 连接");
    }

    // 5. 关闭 WebSocket 信令连接
    if (ws) {
        ws.close();
        ws = null;
        console.log("已关闭 WebSocket 连接");
    }

    // 6. 更新 UI
    statusDiv.textContent = "状态: 已离开频道";
    document.getElementById('startBtn').disabled = false;
    document.getElementById('leaveBtn').disabled = true;
    document.getElementById('videoBtn').disabled = true;
    pttBtn.disabled = true;

    // 停止统计
    if (statsInterval) {
        clearInterval(statsInterval);
        statsInterval = null;
    }

    // 重置显示
    document.getElementById('statsUp').textContent = '0 MB';
    document.getElementById('statsDown').textContent = '0 MB';
    document.getElementById('statsTotal').textContent = '0 MB';

    videoEnabled = false;

    document.getElementById('nameInput').disabled = false;
    document.getElementById('channelInput').disabled = false;

    // 可以选择清空聊天记录
    // messages.innerHTML = ''; 
}

// 发送文本消息保持不变
function sendMessage () {
    let input = document.getElementById("messageInput");
    if (dc && dc.readyState === "open") {
        let message = document.createElement('div');
        message.textContent = '发送: ' + input.value;
        messages.appendChild(message);
        messages.scrollTop = messages.scrollHeight;
        dc.send(JSON.stringify({ nick: nickname, text: input.value }));
        input.value = "";
    } else {
        console.warn("数据通道尚未就绪，请稍等...");
    }
}

async function openVideo () {
    document.getElementById('videoBtn').disabled = true;
    if (!localStream || videoEnabled) {
        return;
    }

    try {
        statusDiv.textContent = "状态：正在请求摄像头权限...";

        const videoStream = await navigator.mediaDevices.getUserMedia({
            video: {
                width: { ideal: 640, max: 640 },
                height: { ideal: 360, max: 360 },
                frameRate: { ideal: 15, max: 15 },
                facingMode: "user"
            },
            audio: false // 不重复获取音频
        });

        videoStream.getVideoTracks().forEach(track => {
            localStream.addTrack(track);
            pc.addTrack(track, localStream);
        });

        if (localVideo) {
            localVideo.srcObject = localStream;
        }
        forceVP8Codec();

        videoEnabled = true;
        statusDiv.textContent = "状态：视频已开启";
        document.getElementById('videoBtn').disabled = true;
        document.getElementById('videoBtn').textContent = "视频已开启";

        if (pc && ws && ws.readyState === WebSocket.OPEN) {
            const offer = await pc.createOffer();
            const modifiedSdp = limitVideoBandwidthInSdp(offer.sdp, VideoBitrate);
            await pc.setLocalDescription({ type: offer.type, sdp: modifiedSdp });
            ws.send(JSON.stringify(offer));
            console.log("视频轨道已添加，重新发送 Offer");
        }

    } catch (err) {
        console.error("无法获取摄像头权限或初始化失败:", err);
        statusDiv.textContent = "状态: 错误 - " + err.message;
        document.getElementById('startBtn').disabled = false;
        document.getElementById('leaveBtn').disabled = true;
        pttBtn.disabled = true;
    }
}

async function updateStats () {
    if (!pc || pc.connectionState !== 'connected') {
        return;
    }

    try {
        const stats = await pc.getStats();
        let bytesSent = 0;
        let bytesReceived = 0;

        stats.forEach(report => {
            if (report.type === 'outbound-rtp') {
                bytesSent += report.bytesSent || 0;
            }
            if (report.type === 'inbound-rtp') {
                bytesReceived += report.bytesReceived || 0;
            }
        });

        // 转换为 MB 显示
        const upMB = (bytesSent / 1024 / 1024).toFixed(2);
        const downMB = (bytesReceived / 1024 / 1024).toFixed(2);
        const totalMB = ((bytesSent + bytesReceived) / 1024 / 1024).toFixed(2);

        document.getElementById('statsUp').textContent = `${upMB} MB`;
        document.getElementById('statsDown').textContent = `${downMB} MB`;
        document.getElementById('statsTotal').textContent = `${totalMB} MB`;
    } catch (err) {
        console.error('获取统计失败:', err);
    }
}

function limitVideoBandwidthInSdp (sdp, kbps) {
    return sdp.replace(
        /^(m=video.*$)/m,
        `$1\r\nb=AS:${kbps}`
    );
}

function enforceBitrate () {
    pc.getSenders().forEach(sender => {
        if (!sender.track) return;

        const params = sender.getParameters();
        if (!params.encodings || params.encodings.length === 0) {
            params.encodings = [{}];
        }

        if (sender.track.kind === 'video') {
            params.encodings[0].maxBitrate = VideoBitrate * 1000; // 500kbps
            params.encodings[0].scaleResolutionDownBy = 1;
        } else if (sender.track.kind === 'audio') {
            params.encodings[0].maxBitrate = 32000; // 音频 32kbps 足够
        }

        sender.setParameters(params).catch(e => console.warn('setParameters 失败:', e));
    });
}

function forceVP8Codec () {
    if (!pc) return;

    const transceivers = pc.getTransceivers();
    transceivers.forEach(transceiver => {
        if (transceiver.sender.track?.kind === 'video' ||
            transceiver.direction === 'recvonly') {

            const capabilities = RTCRtpSender.getCapabilities('video');
            if (!capabilities) return;

            // 把 VP8 排到最前面
            const vp8 = capabilities.codecs.filter(
                c => c.mimeType === 'video/VP8'
            );
            const others = capabilities.codecs.filter(
                c => c.mimeType !== 'video/VP8'
            );

            try {
                transceiver.setCodecPreferences([...vp8, ...others]);
            } catch (e) {
                console.warn('setCodecPreferences 不支持:', e);
            }
        }
    });
}

function appendMessage (text) {
    const div = document.createElement('div');
    div.textContent = text;
    messages.appendChild(div);
    messages.scrollTop = messages.scrollHeight;
}

function showSystemMessage (text) {
    const div = document.createElement('div');
    div.textContent = text;
    div.style.cssText = 'color:#888; font-style:italic; font-size:12px; margin:4px 0;';
    messages.appendChild(div);
    messages.scrollTop = messages.scrollHeight;
}