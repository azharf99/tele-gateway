# Telegram Auction Userbot (Golang)
<a href="https://github.com/azharf99/tele-gateway"><img src="https://img.shields.io/badge/github-black?style=for-the-badge&logo=github&logoColor=white" /></a>
<a href="mailto:[EMAIL_ADDRESS]"><img src="https://img.shields.io/badge/email-red?style=for-the-badge&logo=email&logoColor=white" /></a>
<a href="https://t.me/azhar_faturohman"><img src="https://img.shields.io/badge/telegram-blue?style=for-the-badge&logo=telegram&logoColor=white" /></a>

Userbot Telegram berbasis Go untuk mengotomasi bidding di grup lelang. Proyek ini dibangun menggunakan arsitektur yang bersih (Clean Architecture) sehingga mudah dipelihara dan dikembangkan oleh para programmer maupun kontributor open-source.

## Fitur Utama
- **Keyword Detection**: Mendeteksi barang lelang berdasarkan keyword dari database.
- **Auto Bid**: Membalas pesan otomatis dengan pesan bid yang ditentukan.
- **Stop Keywords**: Berhenti memantau jika ada keyword tertentu (misal: "Sold", "Closed").
- **Anti-Banned**: Delay random (2-5 detik) untuk mensimulasikan interaksi manusia.
- **Clean Architecture**: Kode terstruktur rapi (Enterprise-grade, testable, maintainable).
- **GORM & PostgreSQL**: Persistence layer yang handal.
- **REST API**: Dilengkapi dengan endpoint API (Gin framework) untuk mengelola data bidding dan autentikasi.

---

## Persyaratan Sistem (Prerequisites)

Sebelum menjalankan aplikasi ini, pastikan sistem kamu sudah menginstal:
- **Go** (Minimal versi 1.26) - Untuk local development
- **PostgreSQL** (Minimal versi 13+) - Sebagai database utama
- **Docker & Docker Compose** - Untuk kemudahan deployment (terutama di VPS)
- Akun Telegram (Untuk mendapatkan `API_ID` dan `API_HASH`)

---

## 🛠 Panduan Instalasi Lokal (Local Development)

Jika kamu ingin mengembangkan atau menjalankan aplikasi ini langsung di komputermu, ikuti langkah-langkah berikut:

### 1. Clone Repository
```bash
git clone https://github.com/azharf99/tele-gateway.git
cd tele-gateway
```

### 2. Siapkan Konfigurasi (`.env`)
Salin file `.env.example` menjadi `.env` dan isi dengan konfigurasi yang sesuai.
```bash
cp .env.example .env
```
Dapatkan kredensial Telegram:
- Kunjungi [my.telegram.org](https://my.telegram.org)
- Login dan masuk ke bagian **API development tools**
- Buat aplikasi baru dan catat `app_id` serta `app_hash`
- Masukkan nilai tersebut ke dalam file `.env` kamu di bagian `TELEGRAM_APP_ID` dan `TELEGRAM_APP_HASH`.

Pastikan juga mengatur koneksi database pada bagian `DB_*` di file `.env`.

### 3. Setup Database
Buat database PostgreSQL dengan nama yang sesuai di konfigurasi `.env` kamu (default: `tele_gateway`). Aplikasi menggunakan GORM untuk *auto-migration*, sehingga tabel-tabel seperti `bid_rules` akan otomatis dibuat saat aplikasi pertama kali dijalankan.

### 4. Unduh Dependensi
```bash
go mod download
```

### 5. Jalankan Aplikasi
```bash
go run cmd/tele-gateway/main.go
```
*Catatan:* Pada saat pertama kali dijalankan, kamu akan diminta memasukkan **kode OTP** yang dikirimkan ke Telegram milik nomor yang kamu konfigurasi di `.env` (`PHONE_NUMBER`).

---

## 🚀 Panduan Deployment di VPS (Docker Compose)

Untuk deployment produksi atau di VPS, cara paling mudah adalah menggunakan **Docker Compose**. Proyek ini sudah dilengkapi dengan `Dockerfile` dan `docker-compose.yml`.

### 1. Persiapkan Server VPS
Pastikan Git, Docker, dan Docker Compose sudah terinstal di server VPS kamu.

### 2. Clone Repository di VPS
```bash
git clone https://github.com/azharf99/tele-gateway.git
cd tele-gateway
```

### 3. Konfigurasi Lingkungan (`.env`)
Salin `.env.example` ke `.env` dan sesuaikan nilainya (terutama kredensial Telegram dan konfigurasi keamanan seperti `JWT_SECRET`).
```bash
cp .env.example .env
nano .env
```

### 4. Setup Network & Database Docker (Penting)
Pada file `docker-compose.yml`, aplikasi diatur untuk menggunakan network eksternal bernama `shared-network` dan mencari host database bernama `gothub_db` (ini nama database di docker saya, bisa diubah sesuka hati).

Jika kamu belum memiliki network dan container database tersebut, kamu harus membuatnya terlebih dahulu, atau kamu bisa menyesuaikan file `docker-compose.yml` agar menggunakan database di dalam *stack* yang sama.

Contoh cara membuat network eksternal jika belum ada:
```bash
docker network create shared-network
```

*(Jika ingin PostgreSQL ter-bundle langsung di docker-compose, silakan edit `docker-compose.yml` dan tambahkan service `postgres`).*

### 5. Build dan Jalankan Container
Jalankan aplikasi di background (detached mode):
```bash
docker-compose up -d --build
```

### 6. Login Telegram (Interaktif OTP)
Karena bot ini berjalan sebagai *Userbot*, ia memerlukan input OTP saat pertama kali login. Kamu harus melakukan *attach* ke container untuk memasukkan OTP:
```bash
docker attach tele-gateway
```
1. Masukkan kode OTP Telegram yang dikirimkan ke nomormu.
2. Jika sudah berhasil login, tekan `Ctrl+P` lalu `Ctrl+Q` untuk keluar dari proses *attach* (detach) tanpa menghentikan container.

Informasi sesi Telegram akan disimpan secara persisten di volume lokal folder `./session/session.json`.

---

## Struktur Database (GORM)

Kamu bisa menambahkan aturan bid (Bid Rules) baru melalui API yang tersedia, atau langsung ke database PostgreSQL:
```sql
INSERT INTO bid_rules (keyword, bid_message, stop_keywords, target_group_id, is_active, has_bidded)
VALUES ('iPhone 13', 'Bid 5.000.000', 'Sold,Closed', -100123456789, true, false);
```
