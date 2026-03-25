from __future__ import annotations

import base64
import json
import logging

import httpx

logger = logging.getLogger(__name__)


class GenerationCancelled(Exception):
    """Raised when an in-flight generate_text stream is cancelled by the caller."""


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


async def generate_text(
    base_url: str,
    model: str,
    prompt: str,
    input_text: str,
    is_stopped=None,
) -> str:
    """Stream a text generation from Ollama.

    is_stopped: optional async callable () -> bool.  Checked every 20 chunks.
    Raises GenerationCancelled if the caller signals a stop mid-stream.
    """
    full_prompt = f"{prompt}\n\nInput:\n{input_text}"
    chunks: list[str] = []
    check_interval = 20
    # connect timeout 30s, read timeout 600s — tokens keep the read alive
    async with httpx.AsyncClient(timeout=httpx.Timeout(30.0, read=600.0)) as client:
        async with client.stream(
            "POST",
            f"{base_url}/api/generate",
            json={"model": model, "prompt": full_prompt, "stream": True},
        ) as resp:
            if resp.is_error:
                await resp.aread()
                logger.error("Ollama error %s: %s", resp.status_code, resp.text[:200])
            resp.raise_for_status()
            async for line in resp.aiter_lines():
                if line:
                    data = json.loads(line)
                    chunks.append(data.get("response", ""))
                    if data.get("done"):
                        break
                    if is_stopped and len(chunks) % check_interval == 0:
                        if await is_stopped():
                            logger.info("generate_text: stop detected after %d chunks — closing stream", len(chunks))
                            raise GenerationCancelled()
    return "".join(chunks).strip()


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
