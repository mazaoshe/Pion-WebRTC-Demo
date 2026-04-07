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
CGO_ENABLED=1 go build -o server
```

## Linux（CentOS 7）打包与部署

本项目音频识别链路使用了 `gopkg.in/hraban/opus.v2`（Opus 解码，依赖系统的 `libopus` + `pkg-config`），因此在 Linux 上构建时需要开启 CGO，并安装对应的开发包。

### 1. 在 CentOS 7 安装构建依赖

```bash
sudo yum install -y epel-release

# 编译工具链（gcc/g++/make 等）
sudo yum groupinstall -y "Development Tools"

# CGO 依赖：pkg-config + libopus（含头文件）
sudo yum install -y pkgconfig opus opus-devel
```

如果你的系统没有 `groupinstall`，也可以最小化安装：

```bash
sudo yum install -y gcc gcc-c++ make pkgconfig opus opus-devel
```

### 2. 安装 Go（示例）

按需选择你自己的 Go 安装方式；这里给一个 tar 包方式示例：

```bash
wget https://go.dev/dl/go1.22.4.linux-amd64.tar.gz
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf go1.22.4.linux-amd64.tar.gz
export PATH=/usr/local/go/bin:$PATH
go version
```

### 3. 在 CentOS 7 编译二进制

```bash
go mod download
CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -o server .
```

说明：

- 由于 Opus 解码依赖 CGO，`CGO_ENABLED=0` 会导致构建失败或运行时缺失能力。
- 运行机器需要安装 `opus`（提供 `libopus.so`），否则启动会报找不到共享库。

### 3.1 常见编译问题（CentOS 7：libopus 版本过老）

如果遇到类似错误：

- `undefined reference to 'OPUS_GET_IN_DTX'`

原因通常是：CentOS 7 默认仓库里的 `libopus` 版本较老（常见为 1.0.x），而 `OPUS_GET_IN_DTX` 是在 Opus 1.3 之后才提供的符号。

解决办法：从源码编译安装新版 Opus（例如 1.4），并确保 `pkg-config` 与动态链接器都指向新库。

```bash
# 1) 安装编译工具
sudo yum install -y autoconf automake libtool pkgconfig gcc gcc-c++ make

# 2) 编译安装 opus（示例：1.4，安装到 /usr/local）
cd /tmp
curl -LO https://downloads.xiph.org/releases/opus/opus-1.4.tar.gz
tar xzf opus-1.4.tar.gz
cd opus-1.4
./configure --prefix=/usr/local
make -j2
sudo make install

# 3) 让动态链接器能找到 /usr/local/lib 下的新 libopus.so
echo '/usr/local/lib' | sudo tee /etc/ld.so.conf.d/opus.conf
sudo ldconfig

# 4) 确认 pkg-config 读到的是新版本
export PKG_CONFIG_PATH=/usr/local/lib/pkgconfig:$PKG_CONFIG_PATH
pkg-config --modversion opus
```

如果 `pkg-config --modversion opus` 已经是 1.4，但仍然报同样的链接错误，通常是 Go/CGO 缓存或链接搜索顺序仍然在用旧库。建议按顺序做一次清理与强制重编译：

```bash
go clean -cache
go clean -modcache

export PKG_CONFIG_PATH=/usr/local/lib/pkgconfig:$PKG_CONFIG_PATH
export CGO_CFLAGS="-I/usr/local/include"
export CGO_LDFLAGS="-L/usr/local/lib"

CGO_ENABLED=1 go build -a -o server .
```

补充：链接命令里如果出现 `-lopusfile`，而你的 `opusfile` 是用 yum 安装的旧版本，有可能间接依赖系统旧 opus，导致冲突。一般本项目不需要 `opusfile`；如果你的环境里确实引入了它，建议也用源码编译与 `/usr/local` 下的新 opus 配套的 opusfile。

### 4. 打包发布物（建议 tar.gz）

在项目根目录执行：

```bash
tar -czf release.tgz server static cert.pem key.pem docker-compose.yml
```

把 `release.tgz` 传到服务器后解压：

```bash
tar -xzf release.tgz
```

### 5. 在服务器上运行

确保运行机已安装运行时依赖：

```bash
sudo yum install -y opus
```

然后启动：

```bash
USE_TLS=true TLS_CERT_FILE=cert.pem TLS_KEY_FILE=key.pem ./server
```

### 5. 访问应用

打开浏览器，访问 [http://localhost:8080](http://localhost:8080)。

### 6. 测试

```bash
systemctl restart pion-webrtc.service
```

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

需要搭配：
yum install -y opus-devel
