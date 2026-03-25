from __future__ import annotations

import asyncio
import logging
import os
from contextlib import asynccontextmanager

from fastapi import FastAPI

from adapters.inbound.ui import router as ui_router
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
app.include_router(ui_router)
