#!/usr/bin/env bash
set -euo pipefail
# Run on the target Linux server while the 3-user browser acceptance suite runs.
seconds=${1:-3600}
[[ $seconds =~ ^[0-9]+$ ]] || exit 2
mkdir -p artifacts
output="artifacts/resources-$(date -u +%Y%m%dT%H%M%SZ).jsonl"
end=$((SECONDS+seconds))
while (( SECONDS < end )); do
 python3 - <<'PY' >> "$output"
import datetime,json,subprocess
names=subprocess.check_output(['docker','ps','-q','--filter','label=cloud-browser.managed=true'],text=True).split()
sample={'time':datetime.datetime.now(datetime.timezone.utc).isoformat(),'containers':[]}
if names:
 stats=subprocess.check_output(['docker','stats','--no-stream','--format','{{json .}}',*names],text=True)
 sample['containers']=[json.loads(line) for line in stats.splitlines()]
 for item in sample['containers']:
  name=item['ID']
  item['state']=json.loads(subprocess.check_output(['docker','inspect','--format','{{json .State}}',name],text=True))
print(json.dumps(sample))
PY
 sleep 15
done
echo "Resource samples: $output"
