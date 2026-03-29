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
    document.getElementById('startBtn').disabled = true;
    document.getElementById('leaveBtn').disabled = false;
    document.getElementById('videoBtn').disabled = false;
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
                { urls: ["stun:stun.l.google.com:19302"] }
            ]
        });

        // 3. 将本地音频轨道添加到 PeerConnection 中
        localStream.getTracks().forEach(track => {
            pc.addTrack(track, localStream);
            console.log("本地音频轨道已添加");
        });

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
        dc.onopen = () => console.log("数据通道已成功建立！");
        dc.onmessage = event => {
            let message = document.createElement('div');
            message.textContent = '收到: ' + event.data;
            messages.appendChild(message);
            messages.scrollTop = messages.scrollHeight;
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
        ws = new WebSocket(wsProtocol + "//" + window.location.host + "/ws");

        ws.onopen = function () {
            console.log("WebSocket 连接已建立，正在发起 Offer...");
            pc.createOffer().then(offer => {
                pc.setLocalDescription(offer);
                ws.send(JSON.stringify(offer));
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
        dc.send(input.value);
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
                width: { ideal: 1280 },
                height: { ideal: 720 },
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

        videoEnabled = true;
        statusDiv.textContent = "状态：视频已开启";
        document.getElementById('videoBtn').disabled = true;
        document.getElementById('videoBtn').textContent = "视频已开启";

        if (pc && ws && ws.readyState === WebSocket.OPEN) {
            const offer = await pc.createOffer();
            await pc.setLocalDescription(offer);
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
