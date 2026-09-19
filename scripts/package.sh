#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
npm run build
mkdir -p artifacts
python3 - <<'PY'
import json
from pathlib import Path
from zipfile import ZipFile,ZIP_DEFLATED
version = json.loads(Path('extension/package.json').read_text())['version']
with ZipFile(f'artifacts/cloud-browser-extension-{version}.zip','w',ZIP_DEFLATED) as z:
 for p in Path('extension/dist').rglob('*'):
  if p.is_file():z.write(p,p.relative_to('extension/dist'))
PY
