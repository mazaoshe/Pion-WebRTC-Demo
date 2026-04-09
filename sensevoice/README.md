# 构建 + 启动（后台运行）

docker compose up -d --build

# 看实时日志（首次启动会下载模型，等 2~3 分钟）

docker compose logs -f

# 测试

curl -X POST http://localhost:8765/transcribe -F "file=@test.wav"

常用维护命令
docker compose restart # 重启服务
docker compose down # 停止
docker compose up -d --build # 改了代码后重新构建
