#!/usr/bin/env bash
set -euo pipefail
[[ $EUID == 0 ]] || { echo 'Run as root.' >&2; exit 1; }
: "${AGE_RECIPIENT:?Set the age public recipient; keep the private identity off-server}"
command -v age >/dev/null
root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"
database_mode=$(sed -n 's/^DATABASE_MODE=//p' .env | tail -n1)
if [[ $database_mode != internal ]]; then
 echo 'An existing PostgreSQL is selected. Back up that database separately before running the Profile-only backup.' >&2
 exit 1
fi
compose=(docker compose -f compose.yaml -f compose.database.yaml)
backup_dir=${BACKUP_DIR:-/srv/cloud-browser/backups}
install -d -m 700 "$backup_dir"
work=$(mktemp -d)
restart_services=$("${compose[@]}" ps --services --status running | awk '/^(api|gateway)$/' | tr '\n' ' ')
cleanup(){ rm -rf "$work"; if [[ -n $restart_services ]]; then "${compose[@]}" start $restart_services; fi; }
trap cleanup EXIT
# Deliberately offline: no API writer, viewer or file upload can modify profiles.
"${compose[@]}" stop gateway api
while read -r container; do
 [[ -z $container ]] || docker stop -t 30 "$container" >/dev/null
done < <(docker ps -q --filter label=cloud-browser.managed=true)
# Record the intentional offline stop before capturing the database snapshot.
# Otherwise API reconciliation mistakes a successful backup stop for a crash.
"${compose[@]}" exec -T postgres psql -v ON_ERROR_STOP=1 -U cloudbrowser -d cloudbrowser <<'SQL'
BEGIN;
UPDATE sessions SET state='STOPPED', error='', updated_at=now() WHERE state IN ('RUNNING','STARTING','STOPPING');
DELETE FROM controller_leases WHERE session_id IN (SELECT id FROM sessions WHERE state='STOPPED');
COMMIT;
SQL
"${compose[@]}" exec -T postgres pg_dump -U cloudbrowser -d cloudbrowser -Fc > "$work/database.dump"
tar -C /srv/cloud-browser -czf "$work/profiles.tar.gz" profiles
cp .env "$work/environment.env"
printf '%s\n' "$(date -u +%FT%TZ)" > "$work/created-at"
docker image inspect cloud-browser-browser:local --format '{{.Id}}' > "$work/browser-image-id"
archive="$backup_dir/$(date -u +%Y%m%dT%H%M%SZ).tar.age"
tar -C "$work" -cf - . | age -r "$AGE_RECIPIENT" -o "$archive.tmp"
mv "$archive.tmp" "$archive"
python3 - "$backup_dir" <<'PY'
import pathlib,sys
archives=sorted(pathlib.Path(sys.argv[1]).glob('*.tar.age'),reverse=True)
for file in archives[7:]: file.unlink()
PY
echo "Encrypted backup created: $archive"
