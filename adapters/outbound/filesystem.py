from pathlib import Path


def save_artifact(vault_path: str, artifact_id: str, filename: str, data: bytes) -> str:
    """Save artifact bytes to <vault_path>/artifacts/<artifact_id>/<filename>."""
    out_dir = Path(vault_path) / "artifacts" / artifact_id
    out_dir.mkdir(parents=True, exist_ok=True)
    path = out_dir / filename
    path.write_bytes(data)
    return str(path)
