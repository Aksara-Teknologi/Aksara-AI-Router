# Aksara AI Router: asal kode dan perubahan

Repository ini adalah fork/derivatif dari **QuantumNous/new-api**, bukan produk
resmi atau rilis yang didukung QuantumNous. Baseline awal:
`dd50ee0a567b810ea60e74dcd9ea6a5786b37b91`.

Source upstream, histori Git, nama modul, branding, lisensi AGPL-3.0, NOTICE, serta
atribusi penulis dipertahankan. Fork ini menambahkan profil penggunaan pribadi;
perubahan tidak dinyatakan berasal dari maintainer upstream.

Konfigurasi OAuth provider, installed client, scope, dan envelope Antigravity
mengacu pada **decolua/9router**, revisi
`1339107e624d1ab150c6a0c2c2ee22caa7e9db2f`, berlisensi MIT. Pemberitahuan lisensi
disertakan di `THIRD-PARTY-NOTICES-9ROUTER`.

Tambahan fork:

- OAuth Authorization Code + PKCE untuk channel plugin; state terikat sesi admin,
  channel, konfigurasi dan versi credential, berumur pendek serta sekali pakai.
- Credential AES-256-GCM dengan kunci deployment persisten, refresh per channel,
  endpoint admin dengan verifikasi tambahan, dan redaksi audit tanpa token.
- Plugin native Codex Responses, Claude Messages, Antigravity Gemini, sync/stream.
  Driver tidak menerima refresh token atau installed-client secret.
- Callback localhost opsional untuk browser di perangkat server, dan browser
  bridge opsional untuk desktop remote. Pertukaran kode tetap di server.
- Pool satu proses dengan affinity per agent/session, pembagian sesi baru,
  concurrency per akun, antrean terbatas, cooldown dan failover sebelum streaming.
- Profil personal opt-in, enam channel slot awal, pricing plugin nol untuk
  pencatatan pribadi, installer Termux serta workflow build native Android ARM64.

Kontrak plugin yang ditambah membutuhkan host fork ini. File plugin tetap dapat
diunggah, tetapi stock New API yang belum memiliki ekstensi kontrak akan menolak
field/capability OAuth. Source canonical plugin ada di `plugins/personal-tasks/*-oauth`;
marketplace standalone dihasilkan oleh `scripts/sync-oauth-marketplace.mjs`.

Rujukan keamanan: OWASP Authentication, Session Management dan OAuth2 Cheat Sheets;
ASVS 5.0.0 area V6 Authentication, V7 Session Management, V9 Self-contained Tokens,
V10 OAuth/OIDC dan V11 Cryptography. Rujukan ini adalah panduan desain dan tes,
bukan sertifikasi kepatuhan.

Validasi meliputi Go package/regression tests, race pada lifecycle OAuth, driver
fixtures pada moejs, TypeScript/UI build, SQLite/MySQL/PostgreSQL untuk lifecycle
dan kandidat akun, serta uji routing sesi/capacity. Hasil run CI tertentu adalah
bukti untuk commit tersebut; tidak membuktikan entitlement atau keberhasilan login
akun provider nyata, cache hit provider, atau uptime Android. Catatan hasil akhir
lokal/CI dicantumkan pada handoff repository.
