# Validasi fork Aksara

Validasi lokal pada 2026-11-09:

- `go test ./pkg/jsplugin ./service ./model ./controller ./router ./middleware ./relay/channel/task/jsplugin ./plugins`: seluruh delapan package lulus.
- Lifecycle OAuth dan bootstrap profil diuji dengan SQLite 3.50.4, MySQL 8.4.13,
  serta PostgreSQL 16.15. Meliputi inisialisasi berulang, state/sesi/host yang salah,
  replay, expiry, refresh bersamaan, disconnect, isolasi akun, kandidat grup, profile
  bootstrap idempotent, pelestarian harga admin, dua agent berbeda, affinity sesi,
  dan reservation concurrency saat retry berpindah akun.
- `go test -race ./service -run 'TestPluginOAuth|TestAksaraPersonalProfile' -count=1`: lulus pada SQLite; matrix dialect lain dijalankan terpisah dengan server database nyata.
- `go vet ./pkg/pluginoauth ./service ./middleware ./relay/channel/task/jsplugin`: lulus.
- TypeScript `bunx tsc -b`, lint file UI yang berubah, lima tes dialog OAuth, dan
  production build frontend: lulus.
- Ketiga plugin lolos compiler/validator moejs dan fixture masing-masing 3/3.
  Tes driver Node mencakup payload native, batas request, stream, routing identifiers,
  cache controls dan validasi browser bridge: 11/11.
- Browser Chromium: keenam slot channel terlihat; login admin dan step-up nyata
  bekerja; dialog muat pada viewport 390 px. Transport popup melalui callback Go
  localhost kembali ke aplikasi dengan kode provider tiruan dan finish API tiruan.
  Ini tidak dinyatakan sebagai login provider nyata.
- Browser bridge MV3 diuji dalam Chromium dengan extension worker nyata: callback
  ketiga port kembali ke origin pembuka dan state lokal dihapus setelah dipakai.
- Native build Android ARM64 melalui Android NDK 28.2.13676358, target API 26,
  `GOOS=android`, `CGO_ENABLED=1`, PIE: lulus. ELF memakai `/system/bin/linker64`,
  bukan interpreter Linux/glibc. Installer shell lolos pemeriksaan sintaks.

Workflow [Android personal server](../.github/workflows/android-personal.yml)
menjalankan ulang pemeriksaan backend, matrix OAuth/profil MySQL/PostgreSQL,
fixture plugin, frontend build, dan cross-build Android pada setiap push main.
Artifact dikaitkan dengan commit run tersebut dan memiliki SHA256SUMS.

Yang belum dibuktikan oleh tes ini: runtime pada HP Termux fisik, uptime Android,
OAuth consent/token exchange dengan akun pengguna nyata, inference melalui akun
langganan nyata, kuota/concurrency resmi penyedia, atau cache hit provider. Pengujian
tersebut membutuhkan perangkat dan persetujuan akun pemiliknya. Fixture dan
mock tidak dipakai sebagai bukti hasil model atau entitlement provider.
