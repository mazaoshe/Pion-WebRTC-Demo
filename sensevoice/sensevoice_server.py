from fastapi import FastAPI, UploadFile, File, HTTPException
from fastapi.responses import JSONResponse
from funasr import AutoModel
import asyncio
import io, soundfile as sf
import uvicorn
import torch

# 限制 torch 线程
torch.set_num_threads(2)

app = FastAPI()

# 全局锁，同一时间只处理一个请求
_lock = asyncio.Lock()

print("Loading model...")
model = AutoModel(
    model="iic/SenseVoiceSmall",
    vad_model="fsmn-vad",
    vad_kwargs={"max_single_segment_time": 30000},
    trust_remote_code=True,
    device="cpu",
)
print("Model ready.")

@app.post("/transcribe")
async def transcribe(file: UploadFile = File(...)):
    # 如果已经有请求在处理，直接拒绝，防止 OOM
    if _lock.locked():
        raise HTTPException(status_code=429, detail="服务忙，请稍后重试")
    
    async with _lock:
        content = await file.read()
        
        # 限制文件大小，防止超长音频撑爆内存（最大 10MB）
        if len(content) > 10 * 1024 * 1024:
            raise HTTPException(status_code=413, detail="音频太长，请控制在30秒以内")
        
        audio, sr = sf.read(io.BytesIO(content))
        if audio.ndim > 1:
            audio = audio[:, 0]

        res = model.generate(
            input=audio,
            cache={},
            language="auto",
            use_itn=True,
            batch_size_s=30,  # 从60降到30，省内存
        )
        text = res[0]["text"] if res else ""
        return JSONResponse({"text": text})

@app.get("/health")
async def health():
    return {"status": "ok", "busy": _lock.locked()}

if __name__ == "__main__":
    # 单 worker，省内存
    uvicorn.run(app, host="0.0.0.0", port=8765, workers=1)