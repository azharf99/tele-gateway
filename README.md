# Telegram Auction Userbot (Golang)

Userbot telegram berbasis Go untuk mengotomasi bidding di grup lelang.

## Fitur
- **Keyword Detection**: Mendeteksi barang lelang berdasarkan keyword di database.
- **Auto Bid**: Membalas pesan otomatis dengan pesan bid yang ditentukan.
- **Stop Keywords**: Berhenti memantau jika ada keyword tertentu (misal: "Sold", "Closed").
- **Anti-Banned**: Delay random (2-5 detik) simulasi manusia.
- **Clean Architecture**: Enterprise-grade, testable, and maintainable.
- **GORM & PostgreSQL**: Persistence layer yang handal.

## Setup
1. Copy `.env.example` menjadi `.env` dan isi kredensialnya.
2. Dapatkan API ID & Hash di [my.telegram.org](https://my.telegram.org).
3. Jalankan `go run cmd/tele-gateway/main.go`.
4. Masukkan kode OTP yang dikirim ke Telegram kamu.

## Struktur Database (GORM)
Bot akan melakukan auto-migrate tabel `bid_rules`.
Kamu bisa menambahkan aturan bid baru langsung ke database:
```sql
INSERT INTO bid_rules (keyword, bid_message, stop_keywords, target_group_id, is_active, has_bidded)
VALUES ('iPhone 13', 'Bid 5.000.000', 'Sold,Closed', -100123456789, true, false);
```
