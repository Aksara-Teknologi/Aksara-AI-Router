# Aksara AI Router di Termux

Fork New API untuk server pribadi Android ARM64, Android 8/API 26 atau lebih baru.
Satu proses Go, SQLite lokal, frontend tertanam dalam binary. Redis, Docker, Bun,
dan program OAuth CLI tidak diperlukan di HP.

## Instalasi baru

Runner GitHub menerbitkan ZIP publik setelah build `main` sukses. Unduh langsung
dari Termux, tanpa PAT atau login GitHub:

```sh
pkg update
pkg install curl openssl-tool termux-services unzip coreutils
mkdir -p "$HOME/aksara-download"
cd "$HOME/aksara-download"
curl -fL https://github.com/Aksara-Teknologi/Aksara-AI-Router/releases/latest/download/aksara-ai-router-android-arm64.zip -o aksara-ai-router.zip
unzip -o aksara-ai-router.zip
sha256sum -c SHA256SUMS
sh install.sh
```

Buka sesi Termux baru agar service manager aktif, kemudian:

```sh
sv up aksara-ai-router
termux-wake-lock
```

Buka **http://localhost:3000** di browser HP dan selesaikan setup administrator.

## Memperbarui instance New API yang sudah ada

Jangan jalankan `install.sh` untuk instance lama karena installer itu membuat
direktori dan database baru. Unduh dan verifikasi paket dengan perintah yang sama
di atas, lalu backup database SQLite instance lama. Jika memakai lokasi default:

```sh
sv down new-api
cp "$HOME/new-api/new-api.db" "$HOME/new-api/new-api.db.before-aksara"
```

Jika database Anda berada di path lain, backup file pada `SQLITE_PATH` yang dipakai
service. Setelah itu, dari direktori paket hasil ekstrak jalankan:

```sh
sh update-existing.sh "$HOME/new-api/new-api"
sv status new-api
```

Updater mengganti hanya binary `~/new-api/new-api`, menyimpan backup binary lama
di sampingnya, lalu menyalakan kembali service runit `new-api`. File konfigurasi dan
database tidak dipindahkan. Pastikan service run script mengatur
`AKSARA_PERSONAL_PROFILE=true` dan `PLUGIN_OAUTH_ENCRYPTION_KEY` dengan nilai tetap
sebelum menghubungkan akun OAuth. Jangan membuat ulang kunci itu setelah akun
terhubung.

Android dapat menghentikan aplikasi di background. Izinkan Termux berjalan tanpa
optimasi baterai. `termux-wake-lock` membantu menjaga CPU aktif tetapi bukan jaminan
uptime Android. Installer tidak mengubah pengaturan Android atau membuka port publik.

Alternatif foreground: jalankan
`~/.local/share/aksara-ai-router/start.sh`.

## Yang disiapkan otomatis

Pada startup pertama dengan `AKSARA_PERSONAL_PROFILE=true`:

- Tiga plugin bawaan: **Codex OAuth**, **Claude OAuth**, **Antigravity OAuth**.
- Enam slot channel: akun **A** dan **B** untuk setiap penyedia, grup `default`,
  priority sama, weight 100. Slot belum login tidak dipilih untuk trafik.
- Retry budget 2. Model bawaan OAuth diberi rasio **0** hanya jika belum memiliki
  harga atau rasio eksplisit; harga yang sudah dikonfigurasi tetap dipakai.
  Rasio New API berlaku per nama model di seluruh channel, jadi profil ini untuk
  instance pribadi. Angka nol adalah kebijakan pencatatan router Anda, bukan
  pernyataan harga penyedia.
- Batas satu inference aktif per akun; sesi hangat menunggu hingga 30 detik bila
  akunnya sibuk, lalu mengembalikan 429 dengan `Retry-After: 1`.

Bootstrap memakai penanda persisten dan tidak mengulang pengaturan setelah Anda
mengubah atau menghapus channel. Ini ditujukan untuk instance pribadi baru, bukan
mengubah konfigurasi production yang sudah ada.

## Menghubungkan akun

Di **Channels**, buka menu slot yang diinginkan → **Provider OAuth account** →
**Connect provider account** → verifikasi admin → **Open provider sign-in**.
Login di situs penyedia dan berikan persetujuan. Tidak perlu mengisi endpoint,
access token, refresh token, atau client ID.

Callback localhost berjalan pada port 1455 (Codex), 54545 (Claude), dan 51121
(Antigravity). Bila login dilakukan di browser pada HP server yang sama, callback
kembali otomatis ke aplikasi. Jika port dipakai aplikasi lain, listener terkait
tidak diambil alih; gunakan cara manual atau hentikan konflik port terlebih dahulu.

Jika login dari komputer lain, callback `localhost` menunjuk ke komputer itu,
bukan HP server. Tempel URL callback lengkap ke dialog koneksi, atau gunakan
browser bridge pada folder `deploy/browser-bridge`. Ekstensi tersebut opsional
untuk Chrome/Edge desktop; Chrome Android tidak memerlukannya untuk login lokal.

Token disimpan terenkripsi di database. Refresh dilakukan sebelum dipakai lagi dan
diserialkan per channel. Account label dari provider tampil bila tersedia. Disconnect
satu channel tidak memutus channel lain. Gunakan akun berbeda untuk slot A/B;
jangan menyalin refresh token yang sama ke beberapa channel.

## Dua agent dan cache

Buat API key **New API** melalui console. Kirim ID percakapan stabil, berbeda
untuk setiap agent/conversation:

```http
X-Agent-Session-Id: agent-A-conversation-123
```

```http
X-Agent-Session-Id: agent-B-conversation-456
```

Router juga mengenali `Session_id`, `Thread_id`, `Session-Id`, `Thread-Id`,
`prompt_cache_key`, `metadata.user_id`, atau `sessionId` bila header eksplisit
tidak disediakan. ID tidak boleh berubah setiap request. Jika beberapa percakapan
berbagi `metadata.user_id`, gunakan header eksplisit agar tidak dianggap satu sesi.

Session key diisolasi menurut user, API key, plugin, grup, dan model, lalu dihash.
Sesi baru memilih priority tertinggi yang memiliki kapasitas, active request paling
sedikit, kemudian jumlah sesi terikat relatif terhadap weight. Dua agent dengan
akun A/B yang kosong akan memakai kapasitas akun berbeda. Request lanjutan tetap
pada akun yang sama selama tersedia. Jika semua akun sibuk, request menunggu;
router tidak menjanjikan kapasitas melebihi jumlah akun yang terhubung.

Binding diperpanjang satu jam sejak request terakhir, maksimal 10.000 sesi. Pool
dan antrean berada dalam memori satu proses; restart mengosongkan binding. Jangan
menjalankan beberapa proses/replica bila mengandalkan batas concurrency ini.
Mode pool ini khusus plugin OAuth bawaan dan mendahului affinity umum New API.
`PLUGIN_OAUTH_POOL_ENABLED=false` mengembalikan routing standar New API.

Codex menerima `prompt_cache_key` stabil jika klien belum mengirimkannya.
Antigravity menerima `sessionId` stabil. Field cache native Claude seperti
`cache_control` tetap diteruskan dari klien. Jika klien belum menentukan cache
di top level atau blok, adaptor Claude OAuth menambahkan cache ephemeral
(TTL default penyedia, biasanya lima menit). Set `PLUGIN_OAUTH_AUTO_CACHE=false`
untuk menonaktifkan penambahan otomatis ini. Kebijakan ini mengikuti
[dokumentasi prompt caching Claude](https://platform.claude.com/docs/en/build-with-claude/prompt-caching).
Pertahankan urutan system prompt,
tools, dan riwayat pesan; letakkan data yang berubah di bagian akhir. Router tidak
menambahkan timestamp ke prompt atau menyatukan konteks agent yang berbeda.

Cache provider bergantung pada prefix, model, TTL, dan kebijakan penyedia. Affinity
menjaga peluang hit, bukan menjamin hit atau memindahkan cache antarakun.
Rate limit/credential failure sebelum inference dapat memindahkan sesi ke akun lain,
dengan konsekuensi cache dingin. Akun yang menerima 429/401/403/503 diberi cooldown
60 detik dalam pool. Setelah upstream stream diterima, inference tidak diulang.

## Endpoint klien

OAuth hanya mengganti autentikasi upstream channel. Semua model tetap dipanggil
melalui endpoint relay New API yang sama, sehingga autentikasi gateway, grup,
distributor, logging, retry, dan billing tetap mengikuti jalur standar.

| Penyedia | Base URL | Format yang didukung |
| --- | --- | --- |
| Codex | `http://localhost:3000/v1` | Chat Completions dan Responses; Chat Completions dikonversi ke Responses upstream |
| Claude | `http://localhost:3000` | Chat Completions, Responses, dan Anthropic Messages (`/v1/messages`) sesuai konversi New API |
| Antigravity (Gemini) | `http://localhost:3000` | Chat Completions, Responses, dan Gemini native (`/v1beta/models/{model}:generateContent` atau `:streamGenerateContent`) |

Contoh request Codex dari Termux, setelah `AKSARA_API_KEY` diisi secara lokal,
menggunakan endpoint standar:

Bearer New API berlaku pada semua endpoint. SDK Claude juga dapat mengirim API
key New API sebagai `x-api-key`; SDK Gemini/Antigravity dapat memakai
`x-goog-api-key` atau `?key=`. Header lebih disarankan daripada query. New API
memvalidasi credential gateway, lalu adaptor provider memakai OAuth channel yang
terpilih untuk autentikasi upstream.

```sh
curl http://localhost:3000/v1/chat/completions \
  -H "Authorization: Bearer $AKSARA_API_KEY" \
  -H 'Content-Type: application/json' \
  -H 'X-Agent-Session-Id: agent-A-conversation-123' \
  -d '{"model":"gpt-6.1-sol","messages":[{"role":"user","content":"Halo"}],"stream":false}'
```

Gunakan model yang tersedia pada akun Anda. Daftar deklarasi bukan jaminan akses
langganan. Codex memakai `store:false`, mengirim SSE ke upstream, lalu membuffer
response terminal bila klien meminta non-stream. Kirim riwayat lengkap, tanpa
`previous_response_id` atau background. Session ID stabil memberi prompt cache key
Codex dan session ID Antigravity tanpa mengirim header sesi mentah ke provider.
Antigravity memublikasikan model Gemini; model Claude hanya dipublikasikan oleh
channel Anthropic supaya distributor tidak mencampur dua penyedia.

## Operasional dan update

Data privat: `~/.local/share/aksara-ai-router/`. File `.env` berisi kunci enkripsi
dan session secret, mode 600. Binary dan start script mode 700. Jangan commit
database atau `.env` ke Git. Backup keduanya sebagai satu pasangan. Hentikan service
saat menyalin file SQLite untuk backup yang konsisten.

```sh
sv status aksara-ai-router
sv down aksara-ai-router
# Download dan verifikasi artifact baru, lalu jalankan installer lagi.
sv up aksara-ai-router
```

Installer mempertahankan database dan kunci. Log di folder `logs` dirotasi oleh
`svlogd`, sekitar 1 MiB per file dan lima file. Untuk mengubah concurrency, edit
`PLUGIN_OAUTH_MAX_CONCURRENCY` dalam `.env` (1–32), kemudian restart service.
Sesuaikan `GOMEMLIMIT` dengan RAM HP; nilai awal 256 MiB adalah soft limit Go,
bukan batas maksimum seluruh konsumsi memori proses.

Default hanya mendengarkan `127.0.0.1:3000`. Untuk klien dari komputer lain, gunakan
reverse proxy/tunnel HTTPS menuju port ini. Callback selalu mengikuti host yang
memulai flow. Atur cookie secure/trusted URL sesuai konfigurasi HTTPS New API.
Tidak ada tunnel publik atau domain yang dibuat otomatis oleh installer.

Build source di workstation/CI: `bun scripts/sync-oauth-marketplace.mjs`, build web,
set `ANDROID_NDK_HOME`, kemudian `sh scripts/build-android.sh`. Bun/frontend tidak
perlu dijalankan di Android. Artifact Android berbeda dari binary Linux ARM64.

## Batas verifikasi

Build native Android dapat diverifikasi di workstation, tetapi keberhasilan
menjalankan service di perangkat Termux dan login/inference akun provider perlu
dites pada HP dan akun Anda. Kode ini tidak menjamin bahwa kebijakan penyedia
mengizinkan seluruh penggunaan langganan melalui aplikasi pihak ketiga.
