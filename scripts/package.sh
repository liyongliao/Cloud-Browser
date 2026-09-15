#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
npm run build
mkdir -p artifacts
python3 - <<'PY'
from pathlib import Path
from zipfile import ZipFile,ZIP_DEFLATED
with ZipFile('artifacts/cloud-browser-extension-0.1.0.zip','w',ZIP_DEFLATED) as z:
 for p in Path('extension/dist').rglob('*'):
  if p.is_file():z.write(p,p.relative_to('extension/dist'))
PY
