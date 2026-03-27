.PHONY: generate generate-server generate-client

# Regenerate both server (Python) and client (TypeScript) from openapi.yaml.
# Prerequisites:
#   pip install -r requirements-dev.txt   (datamodel-code-generator)
#   cd frontend && npm install             (@hey-api/openapi-ts)
generate: generate-server generate-client

# Generate Python Pydantic models from openapi.yaml → adapters/inbound/schemas.py
generate-server:
	datamodel-codegen \
	  --input openapi.yaml \
	  --input-file-type openapi \
	  --output adapters/inbound/schemas.py \
	  --output-model-type pydantic_v2.BaseModel \
	  --snake-case-field \
	  --use-standard-collections \
	  --use-union-operator \
	  --target-python-version 3.11

# Export openapi.json then generate TypeScript client from it
generate-client:
	cd frontend && npm run generate
