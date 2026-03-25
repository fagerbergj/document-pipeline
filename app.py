from __future__ import annotations

import asyncio
import logging
import os
from contextlib import asynccontextmanager
from pathlib import Path

from fastapi import FastAPI
from fastapi.staticfiles import StaticFiles
from fastapi.responses import FileResponse

from adapters.inbound.api import router as api_v1_router
from adapters.inbound.webhook import router as webhook_router
from adapters.outbound.sqlite import Database
from core.domain.pipeline import PipelineConfig
from core.services.worker import run_worker

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s: %(message)s")
logger = logging.getLogger(__name__)


@asynccontextmanager
async def lifespan(app: FastAPI):
    db_path = os.environ.get("DB_PATH", "/data/pipeline.db")
    vault_path = os.environ.get("VAULT_PATH", "/vault")
    ollama_base_url = os.environ.get("OLLAMA_BASE_URL", "http://ollama:11434")

    db = Database(db_path)
    await db.init()
    recovered = await db.reset_running()
    if recovered:
        logger.warning("Startup: reset %d stuck 'running' doc(s) to pending", recovered)
    logger.info("Database ready: %s", db_path)

    config = PipelineConfig.from_yaml("config/pipeline.yaml")
    logger.info("Pipeline loaded: %d stages", len(config.stages))

    app.state.db = db
    app.state.pipeline = config
    app.state.vault_path = vault_path
    app.state.ollama_base_url = ollama_base_url

    worker_task = asyncio.create_task(
        run_worker(config, db, vault_path, ollama_base_url)
    )

    yield

    worker_task.cancel()
    try:
        await worker_task
    except asyncio.CancelledError:
        pass

    await db.close()
    logger.info("Shutdown complete")


app = FastAPI(title="document-pipeline", lifespan=lifespan)
app.include_router(webhook_router)
app.include_router(api_v1_router)

# Serve React app (only if built dist exists)
_FRONTEND_DIST = Path("frontend/dist")
if _FRONTEND_DIST.exists():
    app.mount("/assets", StaticFiles(directory=str(_FRONTEND_DIST / "assets")), name="assets")

    @app.get("/{full_path:path}", include_in_schema=False)
    async def serve_spa(full_path: str):
        # API and webhook routes are already registered, this catches everything else
        index = _FRONTEND_DIST / "index.html"
        return FileResponse(str(index))
