# Third-party components

This project integrates external components as separate packages/processes. Review the actual licenses of pinned releases when distributing images or modified components. A final license for the original Cloud Browser source has not been selected by the project owner.

| Component | Upstream / license source | Use |
|---|---|---|
| KasmVNC 1.5.0 | https://github.com/kasmtech/KasmVNC/blob/v1.5.0/LICENSE.TXT | Server and bundled web client inside the browser image; retain upstream notices and applicable source obligations |
| Google Chrome | https://www.google.com/chrome/terms/ | Installed from Google's signed Linux apt repository; not relicensed by this project |
| Ubuntu | https://ubuntu.com/legal | Browser image base and packaged software; package licenses apply individually |
| Moby seccomp profile | https://github.com/moby/profiles | Vendored default profile with Chrome user-namespace syscall changes; Apache-2.0 |
| Go | https://go.dev/LICENSE | Backend toolchain |
| pgx | https://github.com/jackc/pgx | PostgreSQL driver, MIT |
| coder/websocket | https://github.com/coder/websocket | WebSocket transport, ISC |
| golang.org/x/crypto | https://cs.opensource.google/go/x/crypto | Argon2id, BSD-style |
| React / Vite / TypeScript | Respective package distributions | Web and extension build/runtime |
| Lucide | https://lucide.dev/license | ISC icons |
| Caddy / PostgreSQL | Official image distributions | Gateway and database |

No Kasm Workspaces proprietary API/UI or ancillary platform audio/file service is copied. PulseAudio capture, file gateway and lifecycle management are implemented separately. Lockfiles identify JavaScript/Go dependencies; OS package versions are recorded in built images. Keep the browser image digest with backups to make rollback reproducible.
