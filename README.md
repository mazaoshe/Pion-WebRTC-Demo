# Pion WebRTC Demo

本项目是一个基于 [Pion WebRTC](https://github.com/pion/webrtc) 的轻量级 WebRTC 演示应用，展示了如何使用 Go 语言实现浏览器之间的实时通信（Peer-to-Peer），包括音视频流传输和数据通道功能。

## 功能特性

- 🚀 **高性能 WebRTC 栈**：基于纯 Go 实现的 Pion WebRTC 库。
- 📹 **实时媒体流传输**：支持摄像头和麦克风的实时采集与推送。
- 🔌 **内置信令服务**：通过 HTTP/WebSocket 实现 SDP 和 ICE 候选者的交换。
- 📦 **数据通道支持**：支持通过 DataChannel 发送文本或二进制数据。
- 🛠️ **易于扩展**：代码结构清晰，适合作为学习 WebRTC 或开发即时通讯应用的起点。

## 目录结构

```
pion-webrtc-demo/
├── docker-compose.yml   # Docker 配置文件
├── Dockerfile           # Docker 镜像构建文件
├── go.mod               # Go 模块依赖管理
├── main.go              # 项目主程序
├── server-amd64         # 可执行文件（示例）
└── static/              # 前端静态资源
    ├── index.html       # 前端页面
    └── main.js          # 前端逻辑
```

## 前置要求

在运行本项目之前，请确保您的开发环境满足以下条件：

- **Go**: 版本 >= 1.18（推荐使用最新稳定版）。
- **Node.js & npm**: （可选）如果需要修改前端资源。
- **浏览器**: 支持 WebRTC 的现代浏览器（如 Chrome、Firefox、Safari、Edge）。

## 快速开始

### 1. 克隆项目

```bash
git clone https://github.com/your-repo/pion-webrtc-demo.git
cd pion-webrtc-demo
```

### 2. 安装依赖

确保 Go 环境已配置完成：

```bash
go mod tidy
```

### 3. 运行项目

直接运行主程序：

```bash
USE_TLS=true TLS_CERT_FILE=cert.pem TLS_KEY_FILE=key.pem go run main.go
```

或者使用 Docker：

```bash
docker-compose up
```

### 4. 构建项目

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o server 
```

### 5. 访问应用

打开浏览器，访问 [http://localhost:8080](http://localhost:8080)。

## 常见问题

### 视频卡顿或连接慢
- **网络问题**：检查网络带宽和延迟。
- **编解码器**：尝试降低视频分辨率或帧率。
- **信令延迟**：确保信令服务器运行正常。

### `SendPLI` 方法未定义
- 确保使用的是最新版本的 Pion WebRTC 库。
- 检查代码中是否正确调用了 `RTPReceiver` 的方法。

## 贡献指南

欢迎提交 Issue 或 Pull Request 来改进本项目！请确保您的代码符合以下要求：
- 遵循 Go 的代码风格。
- 提供清晰的提交信息。

## 许可证

本项目基于 [MIT 许可证](LICENSE) 开源。