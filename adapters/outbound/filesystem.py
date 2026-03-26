from pathlib import Path


def save_png(vault_path: str, date_month: str, hash_prefix: str, image_bytes: bytes) -> str:
    out_dir = Path(vault_path) / date_month
    out_dir.mkdir(parents=True, exist_ok=True)
    path = out_dir / f"{hash_prefix}.png"
    path.write_bytes(image_bytes)
    return str(path)


def rename_png(old_path: str, safe_title: str) -> str:
    old = Path(old_path)
    hash_prefix = old.stem.split("_")[0]
    new_path = old.parent / f"{hash_prefix}_{safe_title}.png"
    if old.exists() and old != new_path:
        old.rename(new_path)
    return str(new_path)
