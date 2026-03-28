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
    async with httpx.AsyncClient(timeout=600.0) as client:
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
    on_chunk=None,
    image_bytes: bytes | None = None,
) -> str:
    """Stream a text generation from Ollama.

    is_stopped: optional async callable () -> bool.  Checked every 20 chunks.
    Raises GenerationCancelled if the caller signals a stop mid-stream.
    """
    full_prompt = f"{prompt}\n\n<ocr_text>\n{input_text}\n</ocr_text>" if input_text else prompt
    payload: dict = {"model": model, "prompt": full_prompt, "stream": True}
    if image_bytes:
        payload["images"] = [base64.b64encode(image_bytes).decode()]
    chunks: list[str] = []
    check_interval = 20
    # connect timeout 30s, read timeout 600s — tokens keep the read alive
    async with httpx.AsyncClient(timeout=httpx.Timeout(30.0, read=600.0)) as client:
        async with client.stream(
            "POST",
            f"{base_url}/api/generate",
            json=payload,
        ) as resp:
            if resp.is_error:
                await resp.aread()
                logger.error("Ollama error %s: %s", resp.status_code, resp.text[:200])
            resp.raise_for_status()
            async for line in resp.aiter_lines():
                if line:
                    data = json.loads(line)
                    chunk = data.get("response", "")
                    chunks.append(chunk)
                    if on_chunk and chunk:
                        await on_chunk(chunk)
                    if data.get("done"):
                        break
                    if is_stopped and len(chunks) % check_interval == 0:
                        if await is_stopped():
                            logger.info("generate_text: stop detected after %d chunks — closing stream", len(chunks))
                            raise GenerationCancelled()
    return "".join(chunks).strip()


async def chat_stream(
    base_url: str,
    model: str,
    messages: list[dict],
    is_stopped=None,
    on_chunk=None,
) -> str:
    """Stream a chat completion from Ollama /api/chat.

    messages: list of {"role": "system"|"user"|"assistant", "content": str}
    is_stopped: optional async callable () -> bool, checked every 20 chunks.
    on_chunk: optional async callable (str) called with each token.
    """
    chunks: list[str] = []
    check_interval = 20
    async with httpx.AsyncClient(timeout=httpx.Timeout(30.0, read=600.0)) as client:
        async with client.stream(
            "POST",
            f"{base_url}/api/chat",
            json={"model": model, "messages": messages, "stream": True},
        ) as resp:
            if resp.is_error:
                await resp.aread()
                logger.error("Ollama chat error %s: %s", resp.status_code, resp.text[:200])
            resp.raise_for_status()
            async for line in resp.aiter_lines():
                if line:
                    data = json.loads(line)
                    chunk = (data.get("message") or {}).get("content", "")
                    chunks.append(chunk)
                    if on_chunk and chunk:
                        await on_chunk(chunk)
                    if data.get("done"):
                        break
                    if is_stopped and len(chunks) % check_interval == 0:
                        if await is_stopped():
                            logger.info("chat_stream: stop detected after %d chunks", len(chunks))
                            raise GenerationCancelled()
    return "".join(chunks).strip()


async def generate_embed(
    base_url: str, model: str, text: str, image_bytes: bytes | None = None
) -> list[float]:
    payload: dict = {"model": model, "input": text or " "}
    if image_bytes:
        payload["images"] = [base64.b64encode(image_bytes).decode()]
    async with httpx.AsyncClient(timeout=300.0) as client:
        resp = await client.post(f"{base_url}/api/embed", json=payload)
        if resp.is_error:
            logger.error("Ollama embed error %s: %s", resp.status_code, resp.text[:200])
        resp.raise_for_status()
        data = resp.json()
        # /api/embed returns {"embeddings": [[...]]}
        embeddings = data.get("embeddings") or data.get("embedding")
        if isinstance(embeddings[0], list):
            return embeddings[0]
        return embeddings


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
