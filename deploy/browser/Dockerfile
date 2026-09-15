FROM --platform=$BUILDPLATFORM golang:1.25-bookworm AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/agent cmd/agent
COPY internal internal
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -o /agent ./cmd/agent
FROM ubuntu:24.04
ARG TARGETARCH
ARG KASMVNC_VERSION=1.5.0
ARG KASMVNC_SHA256=f599fe02e2175b9817b6165f74a5d2bebdc73118dde9181ba3410963bed7ae1e
ENV DEBIAN_FRONTEND=noninteractive HOME=/home/browser DISPLAY=:1 LANG=zh_CN.UTF-8 LC_ALL=zh_CN.UTF-8 XDG_RUNTIME_DIR=/tmp/runtime-browser
RUN test "$TARGETARCH" = amd64
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl gnupg locales openbox pulseaudio pulseaudio-utils dbus-x11 gnome-keyring libpam-gnome-keyring libsecret-tools xclip xdotool x11-utils xauth fonts-noto-cjk fonts-noto-color-emoji tini python3 && locale-gen zh_CN.UTF-8 && rm -rf /var/lib/apt/lists/*
RUN curl -fsSL https://dl.google.com/linux/linux_signing_key.pub | gpg --dearmor -o /usr/share/keyrings/google-chrome.gpg && echo 'deb [arch=amd64 signed-by=/usr/share/keyrings/google-chrome.gpg] https://dl.google.com/linux/chrome/deb/ stable main' > /etc/apt/sources.list.d/google-chrome.list && apt-get update && apt-get install -y --no-install-recommends google-chrome-stable && rm -rf /var/lib/apt/lists/*
RUN curl -fsSL "https://github.com/kasmtech/KasmVNC/releases/download/v${KASMVNC_VERSION}/kasmvncserver_noble_${KASMVNC_VERSION}_amd64.deb" -o /tmp/kasm.deb && echo "${KASMVNC_SHA256}  /tmp/kasm.deb" | sha256sum -c - && apt-get update && apt-get install -y /tmp/kasm.deb && rm -rf /var/lib/apt/lists/* /tmp/kasm.deb
RUN userdel -r ubuntu && groupadd -g 1000 browser && useradd -m -u 1000 -g 1000 -s /bin/bash browser
COPY --from=build /agent /usr/local/bin/browser-agent
COPY deploy/browser/entrypoint.sh deploy/browser/desktop.sh /opt/cloud-browser/
COPY deploy/browser/kasmvnc.yaml /opt/cloud-browser/kasmvnc.yaml
RUN chmod 755 /opt/cloud-browser/*.sh
USER 1000:1000
EXPOSE 6901 8081
ENTRYPOINT ["/usr/bin/tini","--","/opt/cloud-browser/entrypoint.sh"]
