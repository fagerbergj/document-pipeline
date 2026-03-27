#!/usr/bin/env python3
"""Export the OpenAPI schema to openapi.json without starting the server."""
import json
import sys
import os

# Minimal stubs so the app module loads without real DB/config
os.environ.setdefault("DB_PATH", "/tmp/schema-export.db")
os.environ.setdefault("VAULT_PATH", "/tmp/vault")

from app import app

schema = app.openapi()
out = "openapi.json"
with open(out, "w") as f:
    json.dump(schema, f, indent=2)

print(f"Wrote {out}  ({len(schema['paths'])} paths, openapi={schema['openapi']})")
