#!/bin/bash
set -euo pipefail
if [ -z "${DBUS_SESSION_BUS_ADDRESS:-}" ]; then
  exec dbus-run-session -- "$0"
fi
pulseaudio --start --exit-idle-time=-1
pactl load-module module-null-sink sink_name=cloud sink_properties=device.description=CloudBrowser >/dev/null
pactl set-default-sink cloud
# Stable per-profile keyring secret; retained with the profile and never sent to the client.
export CHROME_PROFILE="$HOME/.cloud-browser/chrome-profile"
mkdir -p "$HOME/.cloud-browser" "$CHROME_PROFILE/Default"
if [ ! -f "$HOME/.cloud-browser/keyring-secret" ]; then
  head -c 32 /dev/urandom | base64 > "$HOME/.cloud-browser/keyring-secret"
fi
chmod 600 "$HOME/.cloud-browser/keyring-secret"
eval "$(gnome-keyring-daemon --unlock --components=secrets < "$HOME/.cloud-browser/keyring-secret")"
export GNOME_KEYRING_CONTROL
# Modify preferences only while Chrome is stopped. Leave site data intact.
python3 - <<'PY'
import json,os
path=os.path.join(os.environ['CHROME_PROFILE'],'Default','Preferences')
try:
 with open(path) as f: data=json.load(f)
except FileNotFoundError: data={}
data.setdefault('session',{})['restore_on_startup']=1
data.setdefault('download',{})['default_directory']=os.path.expanduser('~/Downloads')
data['download']['prompt_for_download']=False
with open(path+'.tmp','w') as f: json.dump(data,f)
os.replace(path+'.tmp',path)
PY
openbox &
# Persistent container hostname permits safe recovery from Chrome's stale singleton lock.
rm -f "$CHROME_PROFILE/SingletonLock" "$CHROME_PROFILE/SingletonSocket" "$CHROME_PROFILE/SingletonCookie"
exec google-chrome-stable --user-data-dir="$CHROME_PROFILE" --password-store=gnome-libsecret --remote-debugging-address=127.0.0.1 --remote-debugging-port=9222 --no-first-run --disable-default-apps --start-maximized --restore-last-session
