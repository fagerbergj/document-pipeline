from __future__ import annotations

import base64
import logging

import httpx

logger = logging.getLogger(__name__)


async def generate_vision(base_url: str, model: str, prompt: str, image_bytes: bytes) -> str:
    image_b64 = base64.b64encode(image_bytes).decode()
    async with httpx.AsyncClient(timeout=180.0) as client:
        resp = await client.post(
            f"{base_url}/api/generate",
            json={"model": model, "prompt": prompt, "images": [image_b64], "stream": False},
        )
        if resp.is_error:
            logger.error("Ollama error %s: %s", resp.status_code, resp.text[:200])
        resp.raise_for_status()
        return resp.json().get("response", "").strip()


async def unload_model(base_url: str, model: str):
    try:
        async with httpx.AsyncClient(timeout=10.0) as client:
            await client.post(
                f"{base_url}/api/generate",
                json={"model": model, "keep_alive": 0},
            )
        logger.info("Unloaded model: %s", model)
    except Exception as exc:
        logger.warning("Failed to unload model %s: %s", model, exc)
