FROM python:3.10-slim

WORKDIR /app

# 换阿里云 apt 源
RUN sed -i 's/deb.debian.org/mirrors.aliyun.com/g' /etc/apt/sources.list.d/debian.sources \
    && apt-get update && apt-get install -y \
    libsndfile1 \
    ffmpeg \
    && rm -rf /var/lib/apt/lists/*

# 换阿里云 pip 源，安装依赖
RUN pip install --no-cache-dir \
    -i https://mirrors.aliyun.com/pypi/simple/ \
    torch torchaudio --extra-index-url https://download.pytorch.org/whl/cpu

RUN pip install --no-cache-dir \
    -i https://mirrors.aliyun.com/pypi/simple/ \
    funasr modelscope fastapi uvicorn python-multipart soundfile

COPY sensevoice_server.py .

EXPOSE 8765

CMD ["python", "sensevoice_server.py"]