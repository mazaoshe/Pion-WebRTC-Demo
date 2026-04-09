from fastapi import FastAPI, UploadFile, File, HTTPException
from fastapi.responses import JSONResponse
from funasr import AutoModel
import asyncio
import io, soundfile as sf
import uvicorn
import torch
import os
import time
import numpy as np

torch_threads = int(os.environ.get("TORCH_NUM_THREADS", "2"))
torch_interop_threads = int(os.environ.get("TORCH_NUM_INTEROP_THREADS", "1"))
torch.set_num_threads(torch_threads)
torch.set_num_interop_threads(torch_interop_threads)

app = FastAPI()

# 全局锁，同一时间只处理一个请求
_lock = asyncio.Lock()

print("Loading model...")
batch_size_s = int(os.environ.get("BATCH_SIZE_S", "15"))
vad_max_single_segment_time = int(os.environ.get("VAD_MAX_SINGLE_SEGMENT_TIME", "15000"))
lock_mode = os.environ.get("LOCK_MODE", "wait").strip().lower()
lock_timeout_s = float(os.environ.get("LOCK_TIMEOUT_S", "0"))
resample_to_16k = os.environ.get("RESAMPLE_TO_16K", "0").strip().lower() in ("1", "true", "yes")

model = AutoModel(
    model="iic/SenseVoiceSmall",
    vad_model="fsmn-vad",
    vad_kwargs={"max_single_segment_time": vad_max_single_segment_time},
    trust_remote_code=True,
    device="cpu",
)
print("Model ready.")

@app.post("/transcribe")
async def transcribe(file: UploadFile = File(...)):
    if lock_mode == "reject" and _lock.locked():
        raise HTTPException(status_code=429, detail="服务忙，请稍后重试")

    start = time.perf_counter()

    if lock_timeout_s > 0:
        try:
            await asyncio.wait_for(_lock.acquire(), timeout=lock_timeout_s)
        except asyncio.TimeoutError:
            raise HTTPException(status_code=429, detail="服务忙，请稍后重试")
        try:
            content = await file.read()
            if len(content) > 10 * 1024 * 1024:
                raise HTTPException(status_code=413, detail="音频太长，请控制在30秒以内")

            audio, sr = sf.read(io.BytesIO(content))
            if audio.ndim > 1:
                audio = audio[:, 0]

            audio = np.asarray(audio)
            if audio.dtype != np.float32:
                audio = audio.astype(np.float32, copy=False)

            if resample_to_16k and sr != 16000:
                import torchaudio
                wav = torch.from_numpy(audio)
                wav = torchaudio.functional.resample(wav, sr, 16000)
                audio = wav.cpu().numpy().astype(np.float32, copy=False)
                sr = 16000

            with torch.inference_mode():
                res = model.generate(
                    input=audio,
                    cache={},
                    language="auto",
                    use_itn=True,
                    batch_size_s=batch_size_s,
                )
            text = res[0]["text"] if res else ""
            return JSONResponse({"text": text})
        finally:
            _lock.release()
            dur_ms = int((time.perf_counter() - start) * 1000)
            if sr and isinstance(sr, (int, float)) and sr > 0:
                audio_s = round(len(audio) / float(sr), 3)
            else:
                audio_s = -1
            print(f"transcribe done in {dur_ms}ms, audio_s={audio_s}, locked={_lock.locked()}")

    async with _lock:
        content = await file.read()
        
        # 限制文件大小，防止超长音频撑爆内存（最大 10MB）
        if len(content) > 10 * 1024 * 1024:
            raise HTTPException(status_code=413, detail="音频太长，请控制在30秒以内")
        
        audio, sr = sf.read(io.BytesIO(content))
        if audio.ndim > 1:
            audio = audio[:, 0]

        audio = np.asarray(audio)
        if audio.dtype != np.float32:
            audio = audio.astype(np.float32, copy=False)

        if resample_to_16k and sr != 16000:
            import torchaudio
            wav = torch.from_numpy(audio)
            wav = torchaudio.functional.resample(wav, sr, 16000)
            audio = wav.cpu().numpy().astype(np.float32, copy=False)
            sr = 16000
        with torch.inference_mode():
            res = model.generate(
                input=audio,
                cache={},
                language="auto",
                use_itn=True,
                batch_size_s=batch_size_s,
            )
        text = res[0]["text"] if res else ""
        dur_ms = int((time.perf_counter() - start) * 1000)
        if sr and isinstance(sr, (int, float)) and sr > 0:
            audio_s = round(len(audio) / float(sr), 3)
        else:
            audio_s = -1
        print(f"transcribe done in {dur_ms}ms, audio_s={audio_s}, locked={_lock.locked()}")
        return JSONResponse({"text": text})

@app.get("/health")
async def health():
    return {"status": "ok", "busy": _lock.locked()}

if __name__ == "__main__":
    # 单 worker，省内存
    uvicorn.run(app, host="0.0.0.0", port=8765, workers=1)
