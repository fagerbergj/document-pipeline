"""Local PaddleOCR adapter for handwritten text recognition."""

import logging

logger = logging.getLogger(__name__)

try:
    import paddle
    from paddleocr import PaddleOCR

    _paddle_available = True
except ImportError:
    _paddle_available = False
    logger.warning("PaddleOCR not installed.")

_ocr_model = None


async def generate_vision(image_path: str) -> str:
    """
    Run PaddleOCR on image. Model is initialized once per process.

    Args:
        image_path: Path to extracted PNG image

    Returns:
        Extracted text string
    """
    if not _paddle_available:
        raise RuntimeError("paddleocr not installed")

    global _ocr_model

    if _ocr_model is None:
        # Use GPU with minimal memory allocation (~2GB total for OCR)
        num_gpus = paddle.device.cuda.device_count() or 1
        gpu_id = 0
        gpu_mem = 2000

        _ocr_model = PaddleOCR(
            use_angle_cls=True,  # Enable classification for rotation
            lang="en",  # For English handwriting
            use_gpu=True,  # Enable GPU
            gpu_id=gpu_id,  # Use first GPU only (OCR is lightweight)
            gpu_mem=gpu_mem,  # 2GB - plenty for OCR (text recognition)
            det_model_dir=None,  # Auto-download best models
            rec_model_dir=None,
        )
        logger.info(f"PaddleOCR initialized on GPU {gpu_id} with {gpu_mem}MB mem")

    result = _ocr_model.ocr(image_path, cls=True)

    # Extract text — PaddleOCR returns [bbox, (text, confidence)] per line
    text_lines = []
    if result and result[0]:
        for line in result[0]:
            if line and len(line) >= 2:
                text, confidence = line[1]
                if text:
                    text_lines.append(str(text).strip())

    return " ".join(text_lines) if text_lines else "(no text recognised)"
