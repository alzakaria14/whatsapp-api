# WhatsApp API Gateway (unofficial)

Gateway multi-nomor berbasis Go dan WhatsApp Web Multi-Device. Data nomor,
pesan, serta riwayat webhook disimpan di MySQL/MariaDB; kredensial sesi
WhatsApp disimpan lokal di SQLite karena session store resmi `whatsmeow`
hanya mendukung SQLite dan PostgreSQL.

> Proyek ini tidak berafiliasi dengan WhatsApp/Meta. Otomasi WhatsApp Web
> dapat melanggar ketentuan layanan atau menyebabkan akun dibatasi. Gunakan
> hanya untuk nomor dan penerima yang memberi izin.

## Menjalankan

1. Buat database: `CREATE DATABASE whatsapp_gateway CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;`
2. Salin `.env.example` menjadi `.env`, lalu ubah API key, kredensial dashboard, dan DSN.
3. Jalankan `go run ./cmd/server`.
4. Buka `http://localhost:8080`, masuk dengan kredensial dashboard, tambah nomor, lalu scan QR.

Pairing baru mengidentifikasi gateway sebagai **Google Chrome (Windows)** pada
menu Perangkat Tertaut. Sesi lama yang sebelumnya tampil sebagai “Other
device” harus dikeluarkan dan dipairing ulang agar metadata perangkat berubah.

Form **Testing API** tersedia pada tab Pesan. Nomor tujuan dapat ditulis dalam
format internasional (`628...`) atau format lokal Indonesia (`08...`).

Tabel dibuat otomatis saat startup. Folder `data` juga dibuat otomatis.

## API

Semua endpoint API menerima `X-API-Key: <key>` atau
`Authorization: Bearer <key>`.

Kirim pesan:

```bash
curl -X POST http://localhost:8080/api/v1/messages \
  -H "X-API-Key: API_KEY_ANDA" \
  -H "Content-Type: application/json" \
  -d '{"from":"628123456789","to":"628987654321","text":"Halo"}'
```

Lihat nomor dan pesan masuk/keluar:

```bash
curl -H "X-API-Key: API_KEY_ANDA" http://localhost:8080/api/v1/numbers
curl -H "X-API-Key: API_KEY_ANDA" "http://localhost:8080/api/v1/messages?from=628123456789&direction=inbound&limit=50"
```

Webhook menerima JSON untuk setiap pesan masuk. Respons HTTP 2xx dianggap
berhasil. Delivery gagal dicoba ulang secara eksponensial dan dapat dikirim
ulang manual dari dashboard. Header `X-Webhook-Signature` berisi
`sha256=<HMAC-hex>` dari body JSON.

