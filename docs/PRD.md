# Product Requirements Document (PRD)
## Billing & Automation Platform — "WHMCS Clone"

| Field | Detail |
|---|---|
| **Nama Produk** | **WHCMS** — Web Hosting Central Management System |
| **Versi Dokumen** | 1.1 |
| **Tanggal** | 3 Juli 2026 (spesifikasi awal) |
| **Status** | Diimplementasikan — spesifikasi historis; kontrak berjalan ada di `docs/CONTRACTS.md` |
| **Pemilik Produk** | Tim WHCMS |
| **Target Rilis MVP** | Lihat Roadmap §16 |

---

## Daftar Isi

1. [Ringkasan Eksekutif](#1-ringkasan-eksekutif)
2. [Latar Belakang & Pernyataan Masalah](#2-latar-belakang--pernyataan-masalah)
3. [Tujuan, Sasaran & Metrik Keberhasilan](#3-tujuan-sasaran--metrik-keberhasilan)
4. [Ruang Lingkup (In / Out of Scope)](#4-ruang-lingkup)
5. [Persona & Pengguna](#5-persona--pengguna)
6. [Arsitektur Sistem & Justifikasi Stack](#6-arsitektur-sistem--justifikasi-stack)
7. [Requirement Fungsional](#7-requirement-fungsional)
8. [Spesifikasi Integrasi Eksternal](#8-spesifikasi-integrasi-eksternal)
9. [Requirement Non-Fungsional](#9-requirement-non-fungsional)
10. [Requirement UI/UX (Kemiripan dengan WHMCS)](#10-requirement-uiux)
11. [Model Data](#11-model-data)
12. [Desain API](#12-desain-api)
13. [Strategi Testing & Quality Gate](#13-strategi-testing--quality-gate)
14. [DevOps, Docker & Deployment](#14-devops-docker--deployment)
15. [Keamanan & Kepatuhan](#15-keamanan--kepatuhan)
16. [Roadmap & Milestone](#16-roadmap--milestone)
17. [Risiko & Mitigasi](#17-risiko--mitigasi)
18. [Asumsi & Dependensi](#18-asumsi--dependensi)
19. [Lampiran & Glosarium](#19-lampiran--glosarium)

---

## 1. Ringkasan Eksekutif

Produk ini adalah **platform billing & automation untuk penyedia layanan hosting/domain (web host)** yang meniru pengalaman, tata letak, dan gaya visual WHMCS, namun dibangun dengan stack modern berkinerja tinggi (Go Fiber v3, Svelte 5, PostgreSQL 18, Redis).

Nilai inti produk:

- **Otomatisasi penuh siklus order → bayar → provisioning.** Pelanggan memesan layanan, membayar via **Duitku (Payment Gateway Indonesia)**, dan akun hosting/domain langsung aktif otomatis tanpa intervensi manual.
- **Manajemen client terpusat.** Data pelanggan, layanan, invoice, tiket, dan log dalam satu dashboard.
- **Integrasi provisioning** ke **cPanel/WHM** dan **DirectAdmin** (fase awal), serta **registrar domain via RDash** (`api.rdash.id`).
- **Siap skala.** Arsitektur dirancang untuk menangani lonjakan traffic tetap mulus, dengan caching Redis dan connection pooling PostgreSQL.
- **Siap deploy.** Terkontainerisasi penuh (Docker + Compose) agar mudah dijalankan di Coolify/Dokploy.

Kualitas non-negosiasi:

- Backend **unit test coverage WAJIB > 90%**.
- Frontend **E2E test dengan Playwright** yang mencakup seluruh alur fitur.

---

## 2. Latar Belakang & Pernyataan Masalah

WHMCS adalah standar de-facto industri untuk billing hosting, tetapi memiliki keterbatasan yang menjadi peluang produk ini:

| Masalah pada solusi eksisting | Peluang produk ini |
|---|---|
| WHMCS berlisensi berbayar per bulan & closed-source | Kepemilikan penuh atas kode, tanpa biaya lisensi berulang |
| Berbasis PHP monolitik, sulit diskalakan horizontal untuk traffic tinggi | Backend Go stateless + Redis, mudah di-scale horizontal |
| Payment gateway lokal Indonesia sering butuh modul pihak ketiga | Integrasi Duitku native, dioptimalkan untuk pasar Indonesia |
| UI kurang modern & sulit dikustomisasi mendalam | Frontend Svelte 5 reaktif, komponen modular, tetap familiar bagi pengguna WHMCS |

**Pernyataan masalah:** Penyedia hosting di Indonesia membutuhkan sistem billing otomatis yang (a) mendukung metode pembayaran lokal, (b) mengotomasi provisioning hosting & domain, (c) dapat menangani pertumbuhan traffic, dan (d) tidak mengunci mereka pada lisensi mahal — dengan tetap mempertahankan pengalaman kerja yang sudah dikenal (WHMCS-like).

---

## 3. Tujuan, Sasaran & Metrik Keberhasilan

### 3.1 Tujuan Produk

- **G1** — Mengotomasi 100% alur *order-to-activation* untuk produk hosting & domain sehingga tidak perlu tindakan manual pada kondisi *happy path*.
- **G2** — Menyediakan pengalaman admin & client yang **secara visual & fungsional setara WHMCS**.
- **G3** — Menyediakan integrasi pembayaran Duitku yang andal, aman, dan idempoten.
- **G4** — Menjamin kualitas melalui coverage backend >90% dan E2E menyeluruh.
- **G5** — Menyediakan artefak deploy (Docker) yang plug-and-play di Coolify/Dokploy.

### 3.2 Metrik Keberhasilan (Success Metrics)

| Metrik | Target |
|---|---|
| Waktu order → aktivasi otomatis (hosting) | < 60 detik (p95) setelah pembayaran terkonfirmasi |
| Tingkat keberhasilan provisioning otomatis | ≥ 99% (di luar kegagalan sisi panel eksternal) |
| Backend unit test coverage | **> 90%** (gate CI keras/*hard gate*) |
| Cakupan E2E Playwright | 100% alur fungsional kritikal |
| Latensi API p95 (endpoint read) | < 200 ms |
| Uptime target | ≥ 99.9% |
| Konsistensi status pembayaran (tidak ada *double activation*) | 100% (dijaga idempotensi + verifikasi ganda) |

---

## 4. Ruang Lingkup

### 4.1 In Scope (MVP + Fase 1)

- Autentikasi & RBAC (admin, staff, client).
- Manajemen client (CRUD, profil, kontak, sub-akun, catatan).
- Katalog produk: **Shared Hosting**, **Reseller Hosting**, **Domain**, produk *custom/generic*.
- Keranjang & checkout (order layanan baru, addon domain, promo/kupon).
- Pembayaran otomatis via **Duitku API V2** (VA, e-wallet, QRIS, retail, kartu kredit, paylater).
- Invoicing & billing (invoice, proforma, pajak/PPN, kredit/deposit, biaya *setup*, *late fee*).
- Provisioning otomatis: **cPanel/WHM** & **DirectAdmin** (create, suspend, unsuspend, terminate, change package, ganti password).
- Manajemen domain via **RDash** (cek ketersediaan, register, transfer, renew, nameserver, kontak/WHOIS, EPP, DNS).
- Siklus hidup layanan (aktif, suspend, terminate, renew, upgrade/downgrade).
- Support ticketing (departemen, prioritas, lampiran, balasan, status).
- Panel admin & area client (mirip WHMCS).
- Notifikasi email (transaksional & lifecycle).
- Automation/cron (generate invoice, reminder, suspensi otomatis, terminasi otomatis, sinkronisasi status).
- Reporting & dashboard (pendapatan, layanan aktif, invoice, tiket).
- Pengaturan sistem (general, gateway, mail, produk, pajak, template).

### 4.2 Out of Scope (untuk versi ini)

- Panel hosting selain cPanel/WHM & DirectAdmin (Plesk, CyberPanel, dll) — fase berikutnya.
- Registrar selain RDash — fase berikutnya.
- Payment gateway selain Duitku (Midtrans, Xendit, Stripe) — arsitektur *pluggable* disiapkan, implementasi menyusul.
- Modul afiliasi lanjutan, marketplace add-on, dan *drag-and-drop page builder*.
- Aplikasi mobile native.
- Migrasi data langsung dari instance WHMCS (disediakan hanya spesifikasi importer di fase lanjutan).

### 4.3 Prinsip Desain untuk Ekstensibilitas

Meski *out of scope*, arsitektur **wajib** menyediakan abstraksi berbasis *interface* untuk **Payment Gateway**, **Server/Provisioning Module**, dan **Registrar Module**, sehingga penambahan provider baru tidak mengubah *core*.

---

## 5. Persona & Pengguna

### 5.1 Andi — Pemilik/Admin Hosting Provider
Menjalankan bisnis hosting kecil-menengah. Butuh melihat pendapatan, mengelola produk & harga, memantau layanan aktif, dan menyelesaikan kasus pembayaran/provisioning yang gagal. Menghargai dashboard yang informatif dan otomatisasi maksimal.

### 5.2 Sinta — Staff Support/Billing
Menangani tiket, memverifikasi pembayaran manual (jika ada), membantu client dengan layanan mereka. Butuh alur kerja cepat, pencarian client yang tajam, dan hak akses terbatas (RBAC).

### 5.3 Budi — Client (Pelanggan Akhir)
Membeli hosting & domain. Ingin proses beli-bayar-aktif yang instan, melihat invoice, memperpanjang layanan, mengelola domain (nameserver/DNS), dan membuka tiket bila bermasalah. Mengharapkan pengalaman yang jelas dan tepercaya.

### 5.4 Developer/Operator (Internal)
Men-deploy & memelihara sistem. Butuh containerisasi, konfigurasi via environment variable, observability (log/metrik/health check), dan test yang tepercaya.

---

## 6. Arsitektur Sistem & Justifikasi Stack

### 6.1 Diagram Arsitektur (Logis)

```
                         ┌──────────────────────────────────────────────┐
                         │              Client (Browser)                 │
                         │   Svelte 5 SPA/SSR  (Client Area + Admin)     │
                         └───────────────┬──────────────────────────────┘
                                         │ HTTPS (JSON REST)
                                         ▼
                    ┌───────────────────────────────────────┐
                    │  Reverse Proxy / TLS (Traefik/Caddy)   │  ← dikelola Coolify/Dokploy
                    └───────────────────┬───────────────────┘
                                        ▼
              ┌──────────────────────────────────────────────────┐
              │        Backend API — Go Fiber v3 (stateless)      │
              │  ┌────────────┬────────────┬───────────────────┐  │
              │  │ HTTP Layer │  Services   │  Domain / Core     │  │
              │  │ (handlers) │ (use-case)  │  (entities, rules) │  │
              │  └────────────┴─────┬──────┴───────────────────┘  │
              │        Repositories │      Integrations            │
              └───────────┬─────────┴──────────┬──────────────────┘
                          │                    │
             ┌────────────┼──────────┐   ┌─────┴───────────────────────────┐
             ▼            ▼          ▼   ▼        ▼          ▼         ▼
      ┌───────────┐ ┌─────────┐ ┌────────┐ ┌────────┐ ┌─────────┐ ┌─────────┐
      │PostgreSQL │ │  Redis  │ │ Duitku │ │cPanel/ │ │DirectAd-│ │  RDash  │
      │    18     │ │ (cache, │ │  API   │ │  WHM   │ │  min    │ │Registrar│
      │(primary DB)│ │ queue,  │ │  V2    │ │  API   │ │  API    │ │  API    │
      │           │ │ session)│ └────────┘ └────────┘ └─────────┘ └─────────┘
      └───────────┘ └─────────┘
                          ▲
              ┌───────────┴────────────┐
              │  Worker / Scheduler     │  (proses async & cron:
              │  (Go, share codebase)   │   invoice, reminder, suspend,
              └─────────────────────────┘   provisioning retry, sync)
```

### 6.2 Pola Arsitektur

- **Clean/Hexagonal Architecture** pada backend: pemisahan tegas antara *handler* (transport), *service* (use-case), *domain* (entity & business rules), dan *repository/integration* (adapter). Ini krusial untuk mencapai coverage >90% karena logika bisnis dapat diuji tanpa I/O nyata (via mock/interface).
- **Stateless API**: session/token disimpan di Redis, sehingga instance API dapat di-scale horizontal di belakang load balancer.
- **Asynchronous jobs**: provisioning, pengiriman email, dan callback-heavy tasks diproses via antrian (Redis-backed queue, mis. `asynq`) agar request HTTP tetap cepat & tahan-gagal (retry).
- **Pluggable modules**: `PaymentGateway`, `ServerModule`, `RegistrarModule` didefinisikan sebagai Go interface.

### 6.3 Justifikasi Stack

| Komponen | Pilihan | Alasan |
|---|---|---|
| Bahasa/Framework backend | **Go + Fiber v3** | Concurrency ringan (goroutine) untuk banyak koneksi simultan; Fiber v3 cepat & ergonomis; mudah dites |
| Frontend | **Svelte 5 (runes)** | Reaktivitas efisien, bundle kecil, komponen modular; cocok mereplika UI WHMCS yang komponen-berat |
| Database | **PostgreSQL 18** | ACID, relasional kuat untuk billing, JSONB untuk config fleksibel, partisi & indeks kuat untuk skala |
| Cache/Queue/Session | **Redis** | Cache read-heavy, rate limiting, session store, backend antrian job async |
| Kontainerisasi | **Docker + Compose** | Deploy konsisten & mudah di Coolify/Dokploy |

### 6.4 Struktur Repositori (usulan monorepo)

```
/whmcs-clone
├── /backend                 # Go Fiber v3
│   ├── /cmd
│   │   ├── /api             # entrypoint HTTP server
│   │   └── /worker          # entrypoint scheduler & queue worker
│   ├── /internal
│   │   ├── /domain          # entities, value objects, business rules (100% unit-tested)
│   │   ├── /service         # use-cases (unit-tested dgn mock repo/integration)
│   │   ├── /repository      # implementasi PostgreSQL
│   │   ├── /integration     # duitku, cpanel, directadmin, rdash adapters
│   │   ├── /transport/http  # handlers, middleware, router
│   │   └── /platform        # db, redis, config, logger, mailer
│   ├── /migrations          # SQL migrations
│   ├── /test                # helpers, fixtures, integration tests
│   └── go.mod
├── /frontend                # Svelte 5
│   ├── /src
│   │   ├── /lib             # komponen, stores, api client
│   │   ├── /routes          # client area + admin
│   │   └── /themes          # WHMCS-like theme tokens
│   ├── /tests/e2e           # Playwright
│   └── package.json
├── /deploy
│   ├── docker-compose.yml
│   ├── docker-compose.prod.yml
│   └── /env-examples
├── /docs                    # PRD, ADR, API spec (OpenAPI)
└── README.md
```

---

## 7. Requirement Fungsional

> **Konvensi:** setiap requirement diberi ID `FR-<MODUL>-<NNN>`. Prioritas: **P0** (MVP wajib), **P1** (penting), **P2** (nice-to-have). Setiap modul menyertakan *acceptance criteria* utama.

### 7.1 Autentikasi & Otorisasi (AUTH)

| ID | Prioritas | Requirement |
|---|---|---|
| FR-AUTH-001 | P0 | Registrasi client dengan email + password; verifikasi email wajib sebelum order pertama |
| FR-AUTH-002 | P0 | Login untuk client & admin/staff; token berbasis JWT (access + refresh), refresh disimpan/di-revoke via Redis |
| FR-AUTH-003 | P0 | Reset password via email token berkedaluwarsa |
| FR-AUTH-004 | P0 | RBAC: peran minimal **Admin**, **Staff (support/billing)**, **Client**; hak akses granular per modul |
| FR-AUTH-005 | P1 | Two-Factor Authentication (TOTP) untuk admin/staff |
| FR-AUTH-006 | P1 | Rate limiting login & lockout sementara setelah N kegagalan (via Redis) |
| FR-AUTH-007 | P1 | Audit log untuk aksi sensitif (login admin, perubahan harga, refund, terminate) |
| FR-AUTH-008 | P2 | Impersonation: admin "Login as Client" untuk troubleshooting (dengan audit trail) |

**Acceptance:** Client tak terverifikasi tak bisa checkout; staff tak bisa mengakses menu Settings global; semua token dapat di-revoke; percobaan brute-force diblokir sementara.

### 7.2 Manajemen Client (CLI)

| ID | Prioritas | Requirement |
|---|---|---|
| FR-CLI-001 | P0 | CRUD client dengan profil: nama, email, perusahaan, alamat, telepon, negara, mata uang |
| FR-CLI-002 | P0 | Halaman detail client: ringkasan layanan, domain, invoice, tiket, transaksi, catatan |
| FR-CLI-003 | P0 | Pencarian & filter client (nama, email, status, produk yang dimiliki) dengan pagination server-side |
| FR-CLI-004 | P0 | Status client: Active, Inactive, Closed |
| FR-CLI-005 | P1 | Sub-akun/kontak dengan izin per-area (billing, support, produk) |
| FR-CLI-006 | P1 | Catatan internal admin (tidak terlihat client) |
| FR-CLI-007 | P1 | Saldo/kredit (deposit) client yang dapat digunakan untuk membayar invoice |
| FR-CLI-008 | P2 | Ekspor daftar client (CSV) |

**Acceptance:** Halaman detail memuat seluruh entitas terkait client; pencarian 10k+ record tetap < 300ms (p95) berkat indeks & cache.

### 7.3 Katalog Produk & Layanan (PROD)

| ID | Prioritas | Requirement |
|---|---|---|
| FR-PROD-001 | P0 | Grup produk & produk; tipe: Shared Hosting, Reseller Hosting, Domain, Other/Generic |
| FR-PROD-002 | P0 | Model harga: sekali bayar & berulang (bulanan, triwulan, semesteran, tahunan, dst.), biaya *setup*, multi-mata uang |
| FR-PROD-003 | P0 | Pengikatan produk ke **modul server** (cPanel/DA) + paket/*package* pada panel target |
| FR-PROD-004 | P0 | Konfigurasi otomatis vs manual (auto-setup: on payment / on order / manual) |
| FR-PROD-005 | P1 | Configurable Options (mis. pilih lokasi server, jumlah RAM) & add-ons |
| FR-PROD-006 | P1 | Kupon/diskon (persentase/nominal, sekali/berkala, batas pakai, kedaluwarsa) |
| FR-PROD-007 | P1 | Stok/kuota produk (opsional) |
| FR-PROD-008 | P2 | Upgrade/downgrade antar produk dengan kalkulasi *pro-rata* |

**Acceptance:** Admin dapat membuat produk hosting yang terhubung ke package WHM tertentu dan menandainya auto-setup on payment; harga berulang tampil benar di keranjang & invoice.

### 7.4 Order & Checkout (ORD)

| ID | Prioritas | Requirement |
|---|---|---|
| FR-ORD-001 | P0 | Keranjang: tambah produk hosting, addon domain (register/transfer), configurable options |
| FR-ORD-002 | P0 | Alur checkout: pilih siklus tagih, isi domain, terapkan kupon, ringkasan biaya (subtotal, pajak, total) |
| FR-ORD-003 | P0 | Pembuatan **Order** + **Invoice** otomatis saat submit |
| FR-ORD-004 | P0 | Untuk pengguna baru: buat akun client dalam alur checkout |
| FR-ORD-005 | P0 | Status order: Pending, Active, Fraud, Cancelled |
| FR-ORD-006 | P1 | Validasi domain (cek ketersediaan via RDash sebelum menambah ke keranjang) |
| FR-ORD-007 | P1 | Deteksi fraud sederhana (batas order, blacklist email/domain) |

**Acceptance:** Submit order menghasilkan invoice ber-*due date*; setelah invoice lunas, order berpindah ke Active dan provisioning dipicu.

### 7.5 Pembayaran — Duitku (PAY)

| ID | Prioritas | Requirement |
|---|---|---|
| FR-PAY-001 | P0 | Ambil metode pembayaran aktif via Duitku *Get Payment Method* dan tampilkan (nama, ikon, biaya) |
| FR-PAY-002 | P0 | Buat transaksi via Duitku *Inquiry (v2)* → simpan `reference`, tampilkan `paymentUrl`/VA/`qrString` |
| FR-PAY-003 | P0 | Terima **callback** Duitku (POST x-www-form-urlencoded), validasi signature, update status invoice |
| FR-PAY-004 | P0 | Tangani **return URL** (redirect) untuk UX pasca-bayar |
| FR-PAY-005 | P0 | **Idempotensi**: callback ganda tidak menyebabkan aktivasi/pelunasan berulang |
| FR-PAY-006 | P0 | **Verifikasi ganda**: pada callback, panggil *Check Transaction* Duitku sebelum menandai lunas |
| FR-PAY-007 | P0 | Signature dihitung sesuai formula Duitku (MD5/SHA256 sesuai endpoint), API key **tidak pernah** ada di frontend |
| FR-PAY-008 | P1 | *Reconciliation job*: sinkronkan invoice pending yang callback-nya hilang, via Check Transaction terjadwal |
| FR-PAY-009 | P1 | Pembayaran dengan saldo/kredit client (tanpa gateway) |
| FR-PAY-010 | P1 | Refund/mark-as-refunded (manual, tercatat) |
| FR-PAY-011 | P2 | Konfigurasi expiryPeriod per metode |

**Acceptance:** Invoice hanya berpindah ke *Paid* bila signature valid **dan** Check Transaction mengembalikan status sukses; pengiriman callback berulang tidak menggandakan provisioning (lihat §8.1 untuk detail).

### 7.6 Billing & Invoice (BILL)

| ID | Prioritas | Requirement |
|---|---|---|
| FR-BILL-001 | P0 | Generate invoice: item, subtotal, pajak (PPN konfigurabel), diskon, total, due date |
| FR-BILL-002 | P0 | Status invoice: Draft, Unpaid, Paid, Overdue, Cancelled, Refunded |
| FR-BILL-003 | P0 | Invoice berulang otomatis (recurring) menjelang perpanjangan layanan |
| FR-BILL-004 | P0 | Unduh invoice sebagai PDF |
| FR-BILL-005 | P1 | *Late fee* otomatis untuk invoice overdue (konfigurabel) |
| FR-BILL-006 | P1 | Proforma invoice mode (opsional) |
| FR-BILL-007 | P1 | Penomoran invoice berformat & berurutan (konfigurabel, anti-duplikat) |
| FR-BILL-008 | P1 | Multi-mata uang & pembulatan sesuai mata uang |
| FR-BILL-009 | P2 | Catatan kredit (credit note) |

**Acceptance:** Cron harian membuat invoice perpanjangan X hari sebelum jatuh tempo; nomor invoice unik & berurutan meski proses paralel.

### 7.7 Provisioning Hosting (PROV)

| ID | Prioritas | Requirement |
|---|---|---|
| FR-PROV-001 | P0 | Buat akun otomatis di cPanel/WHM & DirectAdmin saat invoice lunas (auto-setup) |
| FR-PROV-002 | P0 | Suspend akun (mis. saat overdue melewati ambang) |
| FR-PROV-003 | P0 | Unsuspend akun (saat dibayar) |
| FR-PROV-004 | P0 | Terminate akun (saat layanan dihentikan) |
| FR-PROV-005 | P0 | Simpan kredensial layanan (username, domain, server, package) — password terenkripsi |
| FR-PROV-006 | P1 | Change package / upgrade-downgrade paket di panel |
| FR-PROV-007 | P1 | Ganti password akun hosting dari panel admin & client |
| FR-PROV-008 | P1 | Single Sign-On ke panel (cPanel/DA) dari client area bila didukung |
| FR-PROV-009 | P1 | Multi-server: pemilihan server (round-robin/kapasitas) per grup produk |
| FR-PROV-010 | P0 | **Retry & error handling**: kegagalan provisioning masuk antrian retry + notifikasi admin, tidak mengubah status pembayaran |

**Acceptance:** Setelah pembayaran, akun hosting aktif < 60 detik (p95); kegagalan panel tidak membalikkan status *Paid* dan memicu retry + alert.

### 7.8 Manajemen Domain — RDash (DOM)

| ID | Prioritas | Requirement |
|---|---|---|
| FR-DOM-001 | P0 | Cek ketersediaan domain (single & bulk) via RDash |
| FR-DOM-002 | P0 | Registrasi domain otomatis saat lunas |
| FR-DOM-003 | P0 | Perpanjangan (renew) domain |
| FR-DOM-004 | P1 | Transfer domain masuk (dengan EPP/Auth code) |
| FR-DOM-005 | P1 | Kelola nameserver dari client area |
| FR-DOM-006 | P1 | Kelola kontak/WHOIS registrant |
| FR-DOM-007 | P1 | Ambil/atur EPP code |
| FR-DOM-008 | P1 | Kelola DNS record & (opsional) DNSSEC via RDash |
| FR-DOM-009 | P1 | Dukungan domain Indonesia yang butuh dokumen (auto-activation flag) |
| FR-DOM-010 | P2 | Domain forwarding & child nameserver |

**Acceptance:** Domain yang tersedia dapat diregistrasi otomatis pasca-bayar; client dapat mengubah nameserver dan perubahan terpropagasi ke RDash.

### 7.9 Siklus Hidup Layanan (SVC)

| ID | Prioritas | Requirement |
|---|---|---|
| FR-SVC-001 | P0 | Status layanan: Pending, Active, Suspended, Terminated, Cancelled |
| FR-SVC-002 | P0 | Auto-suspend saat overdue > ambang; auto-terminate saat suspended > ambang (cron) |
| FR-SVC-003 | P0 | Auto-unsuspend saat invoice perpanjangan dibayar |
| FR-SVC-004 | P1 | Upgrade/downgrade layanan dgn pro-rata & re-provision paket |
| FR-SVC-005 | P1 | Pembatalan layanan oleh client (immediate / end-of-term) |
| FR-SVC-006 | P1 | Riwayat/log aksi per layanan |

**Acceptance:** Transisi status tercatat, memicu aksi panel yang sesuai, dan mengirim notifikasi ke client.

### 7.10 Support Ticketing (TIC)

| ID | Prioritas | Requirement |
|---|---|---|
| FR-TIC-001 | P0 | Buat tiket (client & admin), departemen, subjek, prioritas, status |
| FR-TIC-002 | P0 | Balasan berulir (threaded), status (Open, Answered, Customer-Reply, Closed) |
| FR-TIC-003 | P1 | Lampiran file (validasi tipe & ukuran) |
| FR-TIC-004 | P1 | Penugasan tiket ke staff & catatan internal |
| FR-TIC-005 | P1 | Notifikasi email pada tiket baru/balasan |
| FR-TIC-006 | P2 | (Opsional) email piping / ticket import |

**Acceptance:** Client dapat membuka & membalas tiket; staff menerima notifikasi; status berubah sesuai balasan.

### 7.11 Panel Admin (ADM)

| ID | Prioritas | Requirement |
|---|---|---|
| FR-ADM-001 | P0 | Dashboard: ringkasan pendapatan, order hari ini, invoice unpaid/overdue, tiket terbuka, layanan aktif |
| FR-ADM-002 | P0 | Modul: Clients, Orders, Invoices, Services, Domains, Tickets, Products, Servers, Registrars, Gateways, Settings, Reports |
| FR-ADM-003 | P0 | Aksi cepat provisioning (create/suspend/unsuspend/terminate) manual dari layanan |
| FR-ADM-004 | P1 | Manajemen staff & peran |
| FR-ADM-005 | P1 | Log aktivitas & log integrasi (request/response ke gateway/panel/registrar, ter-*redact*) |

**Acceptance:** Admin dapat menjalankan seluruh siklus bisnis dari panel tanpa akses DB langsung.

### 7.12 Area Client (CLA)

| ID | Prioritas | Requirement |
|---|---|---|
| FR-CLA-001 | P0 | Dashboard client: layanan aktif, domain, invoice unpaid, tiket |
| FR-CLA-002 | P0 | Kelola profil & keamanan (password, 2FA) |
| FR-CLA-003 | P0 | Lihat & bayar invoice; unduh PDF |
| FR-CLA-004 | P0 | Kelola layanan (detail, login panel, ganti password, upgrade) |
| FR-CLA-005 | P0 | Kelola domain (nameserver, DNS, EPP, renew) |
| FR-CLA-006 | P0 | Order layanan/domain baru |
| FR-CLA-007 | P1 | Riwayat transaksi & deposit saldo |

**Acceptance:** Client menuntaskan seluruh journey (beli → bayar → kelola) tanpa bantuan admin pada *happy path*.

### 7.13 Notifikasi (NOTIF)

| ID | Prioritas | Requirement |
|---|---|---|
| FR-NOTIF-001 | P0 | Email transaksional: verifikasi akun, reset password, invoice dibuat, pembayaran diterima, layanan aktif |
| FR-NOTIF-002 | P0 | Email lifecycle: reminder jatuh tempo, overdue, suspensi, terminasi, renew berhasil |
| FR-NOTIF-003 | P1 | Template email dapat diedit (variabel dinamis), multi-bahasa (ID/EN) |
| FR-NOTIF-004 | P1 | Pengiriman via SMTP konfigurabel; antrian & retry |
| FR-NOTIF-005 | P2 | Webhook/Slack untuk event admin (mis. provisioning gagal) |

### 7.14 Automation & Scheduler (CRON)

| ID | Prioritas | Requirement |
|---|---|---|
| FR-CRON-001 | P0 | Generate invoice perpanjangan (X hari sebelum due) |
| FR-CRON-002 | P0 | Kirim reminder (pre-due, due, overdue) |
| FR-CRON-003 | P0 | Auto-suspend & auto-terminate berdasarkan aturan |
| FR-CRON-004 | P0 | Reconciliation pembayaran Duitku (Check Transaction untuk pending) |
| FR-CRON-005 | P1 | Retry job provisioning/domain yang gagal |
| FR-CRON-006 | P1 | Sinkronisasi status/kedaluwarsa domain dari RDash |
| FR-CRON-007 | P1 | Housekeeping (purge token kedaluwarsa, kompresi log) |

**Acceptance:** Scheduler berjalan idempoten; job yang sama tidak dieksekusi ganda saat multi-instance (lock via Redis).

### 7.15 Pengaturan Sistem (SET)

| ID | Prioritas | Requirement |
|---|---|---|
| FR-SET-001 | P0 | General (nama perusahaan, logo, mata uang default, zona waktu, ambang suspend/terminate) |
| FR-SET-002 | P0 | Konfigurasi Payment Gateway (Duitku: merchant code, mode sandbox/production; API key via env) |
| FR-SET-003 | P0 | Konfigurasi Server (cPanel/WHM & DA: host, port, kredensial/token via secret) |
| FR-SET-004 | P0 | Konfigurasi Registrar (RDash: reseller ID, API key via secret, IP whitelist note) |
| FR-SET-005 | P0 | Konfigurasi pajak (PPN %, inklusif/eksklusif) |
| FR-SET-006 | P1 | Konfigurasi email/SMTP & template |
| FR-SET-007 | P1 | Konfigurasi tema/branding |

**Acceptance:** Semua kredensial sensitif dibaca dari environment/secret store, bukan disimpan plaintext di DB.

---

## 8. Spesifikasi Integrasi Eksternal

> Semua integrasi diimplementasikan sebagai **adapter** di balik interface Go. Semua kredensial (API key, token, password) dibaca dari **environment variable / secret store** — tidak pernah dikirim ke frontend dan tidak di-hardcode.

### 8.1 Duitku (Payment Gateway) — API V2

**Base URL**
- Sandbox: `https://sandbox.duitku.com`
- Production: `https://passport.duitku.com`

**Kredensial:** `DUITKU_MERCHANT_CODE`, `DUITKU_API_KEY`, `DUITKU_ENV` (sandbox|production).

**Endpoint yang digunakan:**

| Fungsi | Method & Path | Signature |
|---|---|---|
| Get Payment Method | `POST /webapi/api/merchant/paymentmethod/getpaymentmethod` | `SHA256(merchantcode + amount + datetime + apiKey)` |
| Create Transaction (Inquiry) | `POST /webapi/api/merchant/v2/inquiry` | `MD5(merchantCode + merchantOrderId + paymentAmount + apiKey)` |
| Callback (diterima) | Endpoint kita, `POST x-www-form-urlencoded` | Verifikasi `MD5(merchantCode + amount + merchantOrderId + apiKey)` |
| Check Transaction | `POST /webapi/api/merchant/transactionStatus` | `MD5(merchantCode + merchantOrderId + apiKey)` |

**Alur pembayaran (state machine):**

```
[Invoice Unpaid] --user pilih metode--> Create Transaction (inquiry)
      │                                        │
      │                                simpan reference, merchantOrderId,
      │                                tampilkan paymentUrl/VA/QR
      ▼                                        │
[Awaiting Payment] <-------- user membayar -----┘
      │
      ├── Duitku kirim CALLBACK (POST) ──► validasi signature
      │         │ valid?                          │ tidak valid → 400, log, abaikan
      │         ▼
      │   panggil CHECK TRANSACTION (verifikasi ganda)
      │         │ statusCode 00 (SUCCESS)?
      │         ▼
      │   [cek idempotensi: invoice sudah Paid? → stop]
      │         ▼
      │   tandai Invoice = Paid, catat Transaksi
      │         ▼
      │   enqueue job: aktivasi order → provisioning/registrasi domain
      │
      └── (jika callback hilang) Reconciliation CRON memanggil
          CHECK TRANSACTION berkala untuk invoice Awaiting Payment
```

**Aturan implementasi kritikal:**
- **Idempotensi wajib.** Sebelum memproses callback/verifikasi, cek status invoice; jika sudah `Paid`, abaikan (return 200) tanpa aktivasi ulang. Gunakan *unique constraint* pada `(invoice_id)` transaksi sukses + kunci baris DB (`SELECT ... FOR UPDATE`).
- **Verifikasi ganda.** Callback saja tidak cukup untuk aktivasi; selalu konfirmasi dengan *Check Transaction*.
- **`resultCode` callback:** `00` = success, `01` = failed. **`statusCode` check:** `00` success, `01` pending, `02` canceled.
- **Signature response callback** dibandingkan dengan hasil hitung ulang di server; ketidakcocokan → tolak & log.
- **`merchantOrderId`** dipetakan 1:1 ke invoice kita (unik).
- **Amount** tanpa desimal (integer, IDR).
- Metode pembayaran didukung mengikuti kode Duitku (contoh): `VC` (kartu kredit), `BC/M2/VA/I1/B1/BT/BR` (VA), `OV/DA/SA/LA/LF` (e-wallet), `SP/LQ/NQ` (QRIS), `FT/A2/IR` (retail), `DN/AT` (paylater).

**Acceptance test khusus:** callback dengan signature valid + Check Transaction sukses → invoice Paid + 1x provisioning; callback dikirim 3x → tetap 1x provisioning; callback signature salah → ditolak; invoice tanpa callback → tersinkron oleh reconciliation.

### 8.2 cPanel / WHM

**Auth:** WHM API token (header `Authorization: whm <user>:<token>`), endpoint `https://<host>:2087`.

**Operasi yang dipetakan (WHM API 1):**

| Aksi domain kita | WHM function |
|---|---|
| Create account | `createacct` (user, domain, plan/package, password, contactemail) |
| Suspend | `suspendacct` (user, reason) |
| Unsuspend | `unsuspendacct` (user) |
| Terminate | `removeacct` (user) |
| Change package | `changepackage` (user, pkg) |
| Change password | `passwd` (user, password) |
| Cek keberadaan/usage | `accountsummary` / `listaccts` |
| SSO ke cPanel | `create_user_session` (opsional) |

**Konfigurasi:** `host`, `port` (2087), `username`, `api_token` (secret), `nameservers`, `use_ssl`.

### 8.3 DirectAdmin

**Auth:** HTTP Basic (admin/reseller username + password **atau** login key) ke `https://<host>:2222`. Mendukung respons legacy (URL-encoded) & JSON pada versi modern — adapter menangani keduanya.

**Operasi yang dipetakan:**

| Aksi domain kita | DA command |
|---|---|
| Create account | `CMD_API_ACCOUNT_USER` (action=create, username, email, passwd, domain, package, ip) |
| Suspend/Unsuspend | `CMD_API_SELECT_USERS` (suspend=Suspend/Unsuspend) |
| Terminate | `CMD_API_SELECT_USERS` (delete=yes) |
| Change package | `CMD_API_MODIFY_USER` (action=package) |
| Change password | `CMD_API_USER_PASSWD` |
| Info akun | `CMD_API_SHOW_USER_CONFIG` / `CMD_API_SHOW_USER_USAGE` |

**Konfigurasi:** `host`, `port` (2222), `username`, `login_key/password` (secret), `use_ssl`, `ip_mode`.

### 8.4 RDash (Registrar Domain)

**Base URL:** `https://api.rdash.id/v1/`
**Auth:** **Basic Auth** — `reseller_id : api_key`. **Catatan penting:** RDash mewajibkan **IP whitelist** untuk API key; IP server produksi/worker harus didaftarkan. Simpan `RDASH_RESELLER_ID` & `RDASH_API_KEY` sebagai secret.

**Kapabilitas yang dipetakan (contoh endpoint pola `/v1/...`):**

| Aksi domain kita | RDash |
|---|---|
| Profil akun/saldo | `GET /v1/account/profile` |
| Cek ketersediaan | endpoint domain check/availability |
| Register / Transfer / Renew | endpoint domain register / transfer / renew |
| Nameserver | endpoint change nameserver |
| Kontak/WHOIS registrant | endpoint contact update |
| EPP / Auth code | endpoint EPP |
| DNS / DNSSEC | endpoint DNS management / DNSSEC |
| Child NS & forwarding | endpoint terkait |
| Harga & transaksi | endpoint price / transaction |

> Detail parameter tiap endpoint dikonfirmasi terhadap live tester `https://api.rdash.id/swagger` (butuh IP ter-whitelist). Adapter menyediakan mapping error RDash → error domain kita, serta *retry* untuk kegagalan sementara.

**Auto-activation:** untuk domain Indonesia yang membutuhkan dokumen, hormati flag *auto-activation* RDash; layanan bisa aktif dulu lalu dokumen diunggah/diverifikasi sesuai kebijakan RDash.

*Implementasi FR-DOM-009 (lihat `CONTRACTS.md` §16):* RDash tidak menyediakan API unggah — hanya tautan halaman unggah milik registrar (`POST /domains/{id}/documents/link`), jadi portal klien meminta tautan itu on-demand dan membukanya di tab baru. Status verifikasi (`verification_status`, `status_reason`) disinkronkan dari registrar; penolakan dokumen, penangguhan (hari ke-5) dan penghapusan (hari ke-30) memicu email ke klien. Sesuai dokumentasi, **tidak ada pengembalian dana bila dokumen tidak diunggah atau ditolak**; domain yang ditolak registrar dan ditandai `is_refundable` dikembalikan sebagai kredit akun sebesar yang dibayar klien untuk domain tersebut, dan admin dapat memberi refund manual.

### 8.5 Prinsip Umum Semua Integrasi

- **Timeout & retry** dengan backoff; batas retry jelas; kegagalan permanen → status *Error* + alert admin.
- **Circuit breaker** untuk mencegah cascading failure saat panel/registrar down.
- **Logging ter-redact** (jangan simpan API key/password di log).
- **Sandbox/staging mode** untuk semua integrasi agar E2E & QA aman.
- **Interface abstraction** (`PaymentGateway`, `ServerModule`, `RegistrarModule`) untuk ekstensibilitas.

---

## 9. Requirement Non-Fungsional

### 9.1 Performa & Skalabilitas
- **NFR-PERF-001** — Latensi API p95 < 200 ms untuk endpoint read (dengan cache Redis untuk data panas: katalog produk, metode pembayaran, dashboard aggregate).
- **NFR-PERF-002** — Sistem menangani lonjakan traffic (mis. promo) tetap mulus: API stateless dapat di-scale horizontal; DB memakai *connection pooling* (pgbouncer opsional).
- **NFR-PERF-003** — Operasi berat (provisioning, email, panggilan eksternal) diproses **asinkron** via queue, bukan di request path.
- **NFR-PERF-004** — Query berat diindeks; daftar besar memakai **pagination server-side** + *keyset pagination* bila perlu.

### 9.2 Keandalan & Ketersediaan
- **NFR-REL-001** — Target uptime ≥ 99.9%.
- **NFR-REL-002** — Job idempoten & aman untuk retry; tidak ada efek ganda.
- **NFR-REL-003** — Scheduler multi-instance aman via distributed lock (Redis).
- **NFR-REL-004** — Graceful shutdown (selesaikan in-flight request/job).

### 9.3 Keamanan (ringkas; detail §15)
- **NFR-SEC-001** — Password di-hash (Argon2id/bcrypt); rahasia via env/secret store.
- **NFR-SEC-002** — Semua endpoint sensitif ter-otorisasi (RBAC) & rate-limited.
- **NFR-SEC-003** — Proteksi OWASP Top 10; input tervalidasi & tersanitasi.
- **NFR-SEC-004** — Password layanan hosting & data sensitif client dienkripsi at-rest.

### 9.4 Observability
- **NFR-OBS-001** — Structured logging (JSON) dengan correlation/request ID.
- **NFR-OBS-002** — Health check endpoint (`/healthz`, `/readyz`) untuk orchestrator.
- **NFR-OBS-003** — Metrik (Prometheus-compatible) opsional: latensi, error rate, queue depth.
- **NFR-OBS-004** — Log integrasi eksternal tersimpan (ter-redact) untuk audit & debugging.

### 9.5 Maintainability & Portability
- **NFR-MAINT-001** — Clean architecture, dependensi terinjeksi (mudah di-mock → coverage tinggi).
- **NFR-MAINT-002** — Migrasi DB terversi (mis. `golang-migrate`), reversible.
- **NFR-MAINT-003** — Konfigurasi 12-factor (env-based), tanpa perubahan kode antar-lingkungan.
- **NFR-MAINT-004** — Dokumentasi API (OpenAPI/Swagger) selalu sinkron.

### 9.6 Lokalur & i18n
- **NFR-I18N-001** — Dukungan Bahasa Indonesia (default) & Inggris pada UI & email template.
- **NFR-I18N-002** — Format mata uang IDR & zona waktu Asia/Jakarta sebagai default.

---

## 10. Requirement UI/UX

Tujuan: **secara style & layout benar-benar mirip WHMCS**, agar pengguna eks-WHMCS langsung familiar, sambil terasa modern & responsif.

### 10.1 Struktur Layout (mengikuti WHMCS)

**Admin Area:**
- **Top navbar**: logo, pencarian global, quick-actions, notifikasi, profil admin.
- **Sidebar kiri**: menu modul (Dashboard, Clients, Orders, Billing/Invoices, Services, Domains, Support, Products/Services, Setup/Settings, Reports, Utilities).
- **Content area**: kartu ringkasan (KPI) di dashboard; tabel data padat dengan filter, sort, pagination; halaman detail dengan tab.
- **Breadcrumb** & judul halaman konsisten.

**Client Area:**
- **Header nav**: Home, Services, Domains, Billing, Support, Account.
- **Dashboard client**: kartu ringkasan (layanan aktif, domain, invoice due, tiket).
- **Halaman order/checkout** bergaya WHMCS (grup produk → konfigurasi → keranjang → checkout).

### 10.2 Design System / Tema
- Palet & tipografi menyerupai WHMCS default (warna aksen biru, latar netral, tabel bergaris, badge status berwarna: hijau=Active/Paid, kuning=Pending/Unpaid, merah=Overdue/Suspended/Terminated, abu=Cancelled).
- Komponen Svelte reusable: `DataTable`, `StatCard`, `StatusBadge`, `Modal`, `FormField`, `Tabs`, `InvoiceView`, `Sidebar`, `Toast`.
- **Responsif** penuh (desktop → mobile), tidak seperti WHMCS lama yang kurang mobile-friendly (peningkatan yang diizinkan).
- Dukungan **dark mode** (opsional/P2) tanpa mengubah struktur.

### 10.3 Prinsip UX
- **NFR-UX-001** — Alur order maksimal beberapa langkah jelas; status pembayaran real-time (polling/refresh) di halaman invoice.
- **NFR-UX-002** — Feedback aksi (toast/loading state) di semua operasi async.
- **NFR-UX-003** — Empty states, error states, dan loading skeleton yang informatif.
- **NFR-UX-004** — Aksesibilitas dasar (kontras, label form, keyboard navigation).

> **Catatan hukum/merek:** Meniru *look & feel* dan tata letak diperbolehkan sebagai referensi desain, namun **jangan menyalin aset berhak cipta WHMCS** (logo, ikon, teks, kode). Gunakan aset & ikon berlisensi bebas (mis. ikon open-source) dan salinan teks orisinal.

---

## 11. Model Data

Entitas inti (relasional, PostgreSQL 18). Kolom timestamp (`created_at`, `updated_at`, `deleted_at` soft-delete) dan audit diasumsikan pada semua tabel. Nilai uang disimpan sebagai `numeric`/*minor units* untuk menghindari galat pembulatan.

### 11.1 Entitas Utama

| Entitas | Deskripsi & field kunci |
|---|---|
| `users` | Kredensial & peran (admin/staff/client). email (unik), password_hash, role, status, 2fa_secret |
| `clients` | Profil pelanggan. user_id, company, address, phone, country, currency, credit_balance, status |
| `client_contacts` | Sub-akun/kontak. client_id, permissions (JSONB) |
| `product_groups` | Grup katalog |
| `products` | client-facing product. group_id, type (hosting/reseller/domain/other), module (cpanel/da), package_name, server_group_id, auto_setup, pricing (JSONB per siklus & mata uang), setup_fee |
| `configurable_options` / `product_addons` | Opsi & add-on |
| `orders` | order_number, client_id, status, total, currency |
| `order_items` | order_id, product_id, domain, cycle, price, options (JSONB) |
| `invoices` | invoice_number (unik), client_id, status, subtotal, tax, discount, total, due_date, currency |
| `invoice_items` | invoice_id, description, amount, related_type/related_id |
| `transactions` | invoice_id, gateway (duitku), gateway_reference, merchant_order_id (unik), amount, status, raw (JSONB) |
| `services` (hosting_accounts) | client_id, product_id, server_id, domain, username, password_enc, status, next_due_date, billing_cycle, panel_meta (JSONB) |
| `domains` | client_id, registrar (rdash), name, status, register_date, expiry_date, nameservers (JSONB), auto_renew, epp_meta |
| `servers` | modul cpanel/da. hostname, port, username, token_enc/password_enc, nameservers, capacity, active |
| `server_groups` | strategi pemilihan (round-robin/least-used) |
| `registrars` | konfigurasi RDash (reseller_id, api_key ref, ip_whitelist_note) |
| `gateways` | konfigurasi Duitku (merchant_code, mode, api_key ref) |
| `tickets` | client_id, department_id, subject, status, priority, assigned_to |
| `ticket_replies` | ticket_id, author, message, is_internal, attachments |
| `coupons` | code, type, value, usage_limit, expires_at |
| `email_templates` | key, locale, subject, body (variabel) |
| `settings` | key-value (JSONB) untuk konfigurasi umum |
| `audit_logs` | actor, action, entity, before/after (JSONB), ip, ts |
| `integration_logs` | provider, endpoint, request/response (ter-redact), status, latency |
| `jobs` (bila persisted) | type, payload, status, attempts, run_at (jika tidak sepenuhnya di Redis) |

### 11.2 Relasi Kunci (ringkas)

```
users 1─1 clients 1─* services *─1 products *─1 server_groups 1─* servers
clients 1─* domains *─1 registrars
clients 1─* invoices 1─* invoice_items
invoices 1─* transactions   (transaksi sukses unik per invoice → idempotensi)
clients 1─* orders 1─* order_items
clients 1─* tickets 1─* ticket_replies
```

### 11.3 Indeks & Integritas Penting
- Unik: `users.email`, `invoices.invoice_number`, `orders.order_number`, `transactions.merchant_order_id`, **partial unique** transaksi status=success per `invoice_id`.
- Indeks: `services.next_due_date` (cron billing), `invoices.status+due_date`, `domains.expiry_date`, foreign keys.
- Gunakan **transaksi DB + row lock** pada alur pembayaran/aktivasi untuk cegah race condition.

---

## 12. Desain API

### 12.1 Konvensi
- **REST/JSON**, versi di path: `/api/v1/...`.
- Auth: `Authorization: Bearer <JWT>`; refresh via endpoint khusus; revocation via Redis.
- Response amplop konsisten: `{ "data": ..., "meta": {...}, "error": null }`.
- Error terstruktur: `{ "error": { "code": "...", "message": "...", "details": [...] } }` + HTTP status tepat.
- Pagination: `?page=&per_page=` atau keyset `?cursor=`; sertakan `meta.total`/`meta.next_cursor`.
- Idempotency-Key header didukung untuk operasi yang menciptakan efek samping (order, pembayaran).
- Validasi input ketat; whitelisting field.
- **OpenAPI 3.1** sebagai sumber kebenaran, digenerate & disajikan di `/api/docs`.

### 12.2 Contoh Kelompok Endpoint (indikatif)

```
# Auth
POST   /api/v1/auth/register
POST   /api/v1/auth/login
POST   /api/v1/auth/refresh
POST   /api/v1/auth/forgot-password
POST   /api/v1/auth/reset-password

# Clients (admin)
GET    /api/v1/admin/clients?search=&status=&page=
POST   /api/v1/admin/clients
GET    /api/v1/admin/clients/:id
PATCH  /api/v1/admin/clients/:id

# Catalog & Order
GET    /api/v1/products
POST   /api/v1/domains/check
POST   /api/v1/orders                 # buat order + invoice
GET    /api/v1/invoices/:id

# Payment (Duitku)
GET    /api/v1/payments/methods       # get payment method (server-side signed)
POST   /api/v1/payments/:invoiceId/pay# create transaction (inquiry)
POST   /api/v1/webhooks/duitku/callback  # callback (form-urlencoded)
GET    /api/v1/payments/return        # redirect handler

# Services & Domains (client)
GET    /api/v1/services
POST   /api/v1/services/:id/password
GET    /api/v1/domains
PATCH  /api/v1/domains/:id/nameservers

# Provisioning (admin actions)
POST   /api/v1/admin/services/:id/create|suspend|unsuspend|terminate

# Support
GET/POST /api/v1/tickets
POST   /api/v1/tickets/:id/replies

# Ops
GET    /healthz  /readyz  /metrics
```

---

## 13. Strategi Testing & Quality Gate

> Testing adalah **acceptance criteria kelulusan proyek**, bukan opsional. CI menolak merge bila gate gagal.

### 13.1 Backend — Unit Test (WAJIB coverage > 90%)

- **Cakupan:** seluruh `domain` (business rules), `service` (use-cases), util signature (Duitku), mapping adapter, dan handler. Setiap FR di §7 memiliki test terkait.
- **Isolasi I/O:** repository & integrasi eksternal di-*mock* via interface. HTTP eksternal (Duitku/cPanel/DA/RDash) diuji dengan **httptest server / mock** — tidak memanggil layanan nyata.
- **Kasus wajib diuji (contoh kritikal):**
  - Perhitungan **signature Duitku** (MD5 inquiry, SHA256 get-method, MD5 callback & check) — vektor uji dengan nilai contoh dari dokumentasi.
  - **Idempotensi callback**: callback ganda → 1x aktivasi.
  - **Verifikasi ganda**: callback tanpa konfirmasi Check Transaction tidak mengaktivasi.
  - Signature callback salah → ditolak.
  - Transisi status invoice/order/service (state machine) valid & invalid.
  - Kalkulasi invoice: pajak, diskon/kupon, pro-rata upgrade, late fee, pembulatan mata uang.
  - Penomoran invoice unik saat konkuren.
  - Auto-suspend/terminate berdasarkan ambang.
  - Mapping error adapter (panel/registrar) → domain error + retry vs permanen.
- **Tooling:** `go test -race -coverprofile`, `-covermode=atomic`; laporan coverage terbit di CI.
- **Gate:** pipeline **fail** jika `total coverage < 90%`. Paket kritikal (`domain`, `service`, `payment`) ditargetkan mendekati 100%.
- **Pelengkap:** integration test terhadap PostgreSQL & Redis nyata via **Testcontainers** (repository & flow end-to-end backend) — di luar hitungan gate unit, tetapi dijalankan di CI.

### 13.2 Frontend — E2E (Playwright, mencakup seluruh fitur)

- **Cakupan alur kritikal (minimal):**
  1. Registrasi → verifikasi email (mock) → login.
  2. Order hosting → checkout → bayar (Duitku **sandbox/mock**) → invoice Paid → layanan Active.
  3. Cek domain → order domain → registrasi (RDash mock/sandbox) → domain aktif.
  4. Client kelola layanan (ganti password) & domain (ubah nameserver).
  5. Bayar invoice perpanjangan → unsuspend.
  6. Buka & balas tiket support.
  7. Admin: buat produk, jalankan aksi provisioning manual, lihat dashboard/report.
  8. RBAC: staff tak bisa akses Settings; client tak bisa akses admin.
- **Strategi data:** environment E2E memakai **mode sandbox/mocked** untuk Duitku & panel/registrar (tanpa efek nyata), diseed database khusus.
- **Eksekusi:** headless di CI, lintas browser (Chromium wajib; Firefox/WebKit opsional), dengan trace & screenshot on-failure.

### 13.3 Test Piramida & CI Gate

```
        ┌───────────────┐
        │  E2E Playwright│  (alur pengguna nyata, mock eksternal)
        ├───────────────┤
        │ Integration    │  (Testcontainers: PG + Redis)
        ├───────────────┤
        │  Unit (>90%)   │  (domain + service + adapters, mock I/O)   ← GATE KERAS
        └───────────────┘
```

**Gate CI wajib lulus sebelum merge:** lint (golangci-lint, eslint/svelte-check) → unit test + coverage ≥ 90% → integration test → build → E2E Playlist. Coverage report & Playwright report diunggah sebagai artefak.

---

## 14. DevOps, Docker & Deployment

### 14.1 Kontainerisasi
- **Multi-stage Dockerfile** untuk backend (build Go statis, image akhir *distroless/alpine* kecil).
- **Dockerfile** frontend (build Svelte → disajikan via adapter Node atau static + reverse proxy).
- Image terpisah untuk **api** dan **worker** (share codebase, entrypoint berbeda).

### 14.2 docker-compose (layanan)
```
services:
  api        # Go Fiber (HTTP)
  worker     # scheduler + queue consumer
  frontend   # Svelte (SSR/static)
  postgres   # PostgreSQL 18
  redis      # Redis
  # (reverse proxy & TLS dikelola oleh Coolify/Dokploy)
```
- Semua konfigurasi via **environment variables** (12-factor). Disediakan `.env.example` lengkap (tanpa nilai rahasia).
- **Healthcheck** pada tiap service; `depends_on` dengan kondisi sehat.
- Volume persist untuk data PostgreSQL; Redis opsional persist.

### 14.3 Kesiapan Coolify / Dokploy
- **REQ-DEPLOY-001** — Aplikasi terdefinisi lewat `docker-compose.yml` yang bisa langsung diimpor Coolify/Dokploy.
- **REQ-DEPLOY-002** — Migrasi DB otomatis saat startup (atau job migrate terpisah) — idempoten & aman diulang.
- **REQ-DEPLOY-003** — Env-driven config; secret dikelola lewat fitur secret Coolify/Dokploy, **bukan** di-commit.
- **REQ-DEPLOY-004** — TLS & domain di-terminate oleh proxy platform (Traefik bawaan). Aplikasi menghormati header proxy (X-Forwarded-*).
- **REQ-DEPLOY-005** — Zero-downtime deploy (rolling) didukung karena API stateless & migrasi backward-compatible.
- **REQ-DEPLOY-006** — Health endpoint dipakai platform untuk readiness.

### 14.4 CI/CD (usulan GitHub Actions)
- Pipeline: `lint → test (unit+coverage gate) → integration → build images → e2e → push registry`.
- Deploy dipicu via webhook Coolify/Dokploy atau `deploy hook` pada merge ke `main`/tag rilis.
- **Catatan environment (dari preferensi tim):** simpan token API sebagai **environment variable/secret**, referensikan hanya nama variabelnya di konfigurasi — jangan hardcode. Selaras dengan pola registry + deploy hook yang sudah tim gunakan untuk Dokploy/Coolify.

---

## 15. Keamanan & Kepatuhan

- **SEC-01** — Password: hash Argon2id (atau bcrypt cost memadai). Tidak pernah simpan plaintext.
- **SEC-02** — Rahasia (Duitku API key, WHM token, DA key, RDash key, JWT secret, DB creds) hanya via env/secret manager. Tidak di DB plaintext, tidak di frontend, tidak di log.
- **SEC-03** — Password akun hosting & data sensitif client **dienkripsi at-rest** (mis. AES-GCM dengan key dari secret).
- **SEC-04** — Semua endpoint diotorisasi (RBAC), input divalidasi; proteksi terhadap SQLi (parameterized/ORM), XSS (escaping Svelte + sanitasi), CSRF (untuk cookie-based; token-based bila JWT header).
- **SEC-05** — Rate limiting (login, order, endpoint publik) via Redis; lockout brute-force.
- **SEC-06** — Webhook Duitku diverifikasi signature; endpoint callback tidak mengandalkan input tak-terverifikasi.
- **SEC-07** — CORS ketat; HTTPS wajib (HSTS di proxy).
- **SEC-08** — Audit log untuk aksi sensitif; integration log ter-redact.
- **SEC-09** — Dependency scanning (govulncheck, npm audit) di CI.
- **SEC-10** — Prinsip least-privilege untuk kredensial panel/registrar (gunakan reseller/token terbatas bila memungkinkan).
- **Kepatuhan:** patuhi ketentuan RDash (IP whitelist), ketentuan Duitku, serta hindari penyalinan aset berhak cipta WHMCS (lihat catatan §10.3).

---

## 16. Roadmap & Milestone

> Estimasi indikatif; sesuaikan kapasitas tim. Prioritas P0 = MVP.

| Fase | Fokus | Deliverable utama |
|---|---|---|
| **M0 — Fondasi** | Setup repo, arsitektur, CI, Docker, skema DB, auth/RBAC | Skeleton berjalan, migrasi, auth, pipeline test+coverage gate aktif |
| **M1 — Core Billing** | Client, produk, order, invoice, pajak/kupon | Alur order→invoice lengkap (belum bayar) + unit test |
| **M2 — Pembayaran Duitku** | Get method, inquiry, callback, check, idempotensi, reconciliation | Bayar sandbox → invoice Paid, teruji idempoten |
| **M3 — Provisioning** | cPanel/WHM & DirectAdmin (create/suspend/unsuspend/terminate), queue+retry | Auto-aktivasi hosting pasca-bayar |
| **M4 — Domain RDash** | Cek, register, renew, nameserver, DNS, EPP | Order domain otomatis + kelola domain client |
| **M5 — Lifecycle & Automation** | Recurring invoice, reminder, auto-suspend/terminate, cron | Siklus tagih otomatis end-to-end |
| **M6 — Support & Admin polish** | Ticketing, dashboard, reports, settings, email templates | Panel admin & client area lengkap gaya WHMCS |
| **M7 — Hardening & E2E** | E2E Playwright menyeluruh, security review, performance/load test, dokumentasi deploy | Rilis-candidate siap Coolify/Dokploy |

**Definition of Done (per fitur):** kode + unit test (coverage terjaga >90%) + E2E untuk alur terkait + dokumentasi API + review + lulus CI gate.

---

## 17. Risiko & Mitigasi

| Risiko | Dampak | Mitigasi |
|---|---|---|
| Callback Duitku hilang/terlambat | Invoice tak ter-update, layanan tak aktif | Verifikasi ganda + reconciliation cron (Check Transaction) |
| Double activation dari callback ganda | Akun/domain terbuat ganda, kerugian | Idempotensi + unique constraint + row lock |
| RDash butuh IP whitelist | API gagal di produksi | Dokumentasikan IP server/worker; daftarkan sebelum go-live; health check integrasi |
| Panel eksternal (WHM/DA) down | Provisioning gagal | Queue retry + circuit breaker + alert admin; status *Paid* tidak dibalik |
| Coverage >90% sulit dicapai | Gate CI gagal | Clean architecture + DI + mock sejak awal; tulis test bersamaan fitur (bukan belakangan) |
| Kemiripan WHMCS memicu isu hak cipta | Masalah legal | Tiru layout/UX, jangan salin aset/kode; pakai aset berlisensi bebas |
| Kompleksitas multi-integrasi | Lambatnya delivery | Interface abstraction; kerjakan per-adapter dengan sandbox/mocking |
| Race condition penomoran invoice | Duplikat nomor | Sequence DB + unique constraint + transaksi |
| Kebocoran rahasia | Kompromi keamanan | Secret via env/secret manager, log ter-redact, scanning di CI |

---

## 18. Asumsi & Dependensi

- Akun & kredensial tersedia untuk: **Duitku** (merchant sandbox+production), **RDash** (reseller ID + API key, IP dapat di-whitelist), server **cPanel/WHM** & **DirectAdmin** untuk provisioning/testing.
- Duitku API V2 sesuai dokumentasi resmi (endpoint, signature, callback) per Juli 2026; perubahan skema pihak ketiga akan memicu penyesuaian adapter.
- Detail parameter tiap endpoint RDash mengikuti dokumentasi registrar; verifikasi akhir dilakukan terhadap sandbox/live API milik reseller masing-masing.
- Tim memiliki kapabilitas Go, Svelte, dan DevOps (sesuai profil tim).
- Lingkungan deploy target: Coolify/Dokploy dengan Docker & reverse proxy bawaan.
- PostgreSQL 18 & Redis tersedia sebagai layanan terkelola atau kontainer.

---

## 19. Lampiran & Glosarium

### 19.1 Glosarium
- **Provisioning** — proses otomatis membuat/mengelola akun di panel hosting.
- **Registrar** — penyedia registrasi domain (di sini: RDash).
- **Callback (Duitku)** — notifikasi server-to-server dari Duitku ke sistem kita saat pembayaran terjadi.
- **Idempotensi** — properti operasi yang aman diulang tanpa efek ganda.
- **RBAC** — Role-Based Access Control.
- **Pro-rata** — perhitungan biaya proporsional terhadap sisa periode.
- **EPP/Auth Code** — kode transfer domain.
- **Reconciliation** — proses menyelaraskan status pembayaran kita dengan gateway.

### 19.2 Referensi
- Dokumentasi Duitku API (V2): `https://docs.duitku.com/api/id/`
- Dokumentasi RDash API: `https://docs.rdash.id/en/developer/api`
- cPanel/WHM API & DirectAdmin API (dokumentasi resmi masing-masing vendor)

### 19.3 Riwayat Revisi
| Versi | Tanggal | Perubahan |
|---|---|---|
| 1.0 | 2026-07-03 | Draft awal lengkap untuk review |

---

*Akhir dokumen.*
