# Panduan Upgrade Hosting Custom-Spec (cPanel / DirectAdmin)

Panduan ini menjelaskan alur lengkap upgrade layanan hosting bertipe
**custom spec** (produk *configurable* — pelanggan menentukan sendiri disk,
bandwidth, jumlah database, dst.) melalui **halaman Client Area**: mulai dari
pengajuan upgrade, pembayaran invoice prorata, sampai spesifikasi baru
benar-benar terpasang di control panel (cPanel/WHM atau DirectAdmin).

Alur yang sama juga berlaku untuk upgrade antar-produk flat (paket tetap) —
produk custom-spec hanya menambah langkah pengaturan slider spesifikasi.

---

## 1. Prasyarat (sekali saja, oleh admin)

Upgrade custom-spec membutuhkan produk *configurable* yang sudah dikonfigurasi
lengkap. Di **Admin Area**:

1. **Server & Server Group** — pastikan ada server cPanel/DirectAdmin aktif di
   sebuah server group (Admin → Setup → Servers). Produk asal dan produk tujuan
   upgrade **harus memakai module panel yang sama** (cpanel ↔ cpanel,
   directadmin ↔ directadmin).
2. **Produk configurable** — Admin → Products: buat/ubah produk, centang
   **Configurable (custom spec)**, pilih module + server group, dan isi harga
   dasar per billing cycle (mis. `monthly`).
3. **Spec knobs** — di halaman edit produk, bagian **Specs**: tambahkan knob
   per sumber daya, misalnya:
   | Field | Contoh |
   |---|---|
   | key / provision key | `disk` → kuota disk; `bandwidth`, `databases`, dst. |
   | unit | `gb`, `mb`, atau `count` |
   | included qty | kuota yang sudah termasuk harga dasar (tidak dikenai biaya) |
   | min / max / step | batas slider yang bisa dipilih pelanggan |
   | default qty | nilai awal slider |
   | allow unlimited | izinkan pilihan "unlimited" (sentinel -1) |
4. **Harga spec per cycle** — tiap knob diberi `unit_price` (harga per unit di
   atas *included qty*) dan `unlimited_price` (harga flat bila unlimited),
   per billing cycle. Rumus biaya knob:
   `max(0, qty − included_qty) × unit_price`, atau `unlimited_price` bila
   unlimited dipilih.

> Katalog publik (`GET /products`) otomatis menyertakan definisi spec + harga
> untuk produk configurable, sehingga modal upgrade di client area bisa
> menampilkan slider tanpa konfigurasi tambahan.

---

## 2. Alur upgrade di Client Area

1. **Login** ke client area, buka **Services → My Services**, klik layanan
   yang ingin di-upgrade (status harus **Active**; layanan suspended harus
   dipulihkan dulu).
2. Klik tombol **Upgrade** (`service-upgrade-button`). Modal upgrade terbuka.
3. **Pilih produk tujuan** dan **billing cycle**:
   - Pindah paket: pilih produk lain (module panel harus sama).
   - **Resize** (kasus paling umum untuk custom-spec): pilih **produk yang
     sama** — slider otomatis terisi dengan spesifikasi yang sedang berjalan.
4. **Atur spesifikasi** pada produk configurable: geser slider tiap knob
   (mis. disk 20 GB → 50 GB), atau centang **Unlimited** bila tersedia.
   Estimasi **harga langganan baru per cycle** (harga dasar + biaya semua
   knob) tampil langsung di modal.
5. Klik **Submit**. Backend memvalidasi ulang dan menghitung harga secara
   otoritatif (estimasi browser tidak pernah dipercaya):
   - Knob yang tidak diubah memakai nilai default/terisi.
   - Kombinasi produk+cycle+spec yang identik dengan konfigurasi berjalan
     ditolak (bukan upgrade).

### Perhitungan prorata

Selisih ditagihkan/dikreditkan **prorata terhadap sisa periode berjalan**
(sampai `next_due_date`):

```
nilai_sisa   = Prorate(harga_lama,  cycle_lama,  sekarang, next_due_date)
tagihan_baru = Prorate(harga_baru,  cycle_baru,  sekarang, next_due_date)
selisih      = tagihan_baru − nilai_sisa
```

- **Selisih > 0 (upgrade)** → dibuat **invoice selisih prorata** dan upgrade
  disimpan sebagai *pending* sampai invoice dibayar. Anda otomatis diarahkan
  ke halaman invoice.
- **Selisih ≤ 0 (downgrade)** → perubahan **langsung diterapkan** dan
  kelebihannya masuk **saldo kredit** akun (tidak pernah ada invoice minus).

Contoh: paket Rp100.000/bln, sisa 15 hari; upgrade disk sehingga harga baru
Rp160.000/bln → tagihan ± (160.000 − 100.000) × 15/30 = **Rp30.000**.

---

## 3. Pembayaran

1. Dari halaman invoice, pilih metode pembayaran lalu bayar:
   - **Duitku** — Virtual Account (BCA/dst.), QRIS, kartu kredit; instruksi
     VA/QRIS tampil langsung di halaman invoice.
   - **Transfer bank manual** — ikuti instruksi rekening; admin
     mengonfirmasi pembayaran di Admin → Transactions.
   - **Saldo kredit** akun bila mencukupi.
2. Selama invoice belum dibayar, halaman layanan menampilkan banner
   **upgrade menunggu pembayaran** beserta tautan invoice
   (`service-upgrade-invoice-link`). Satu layanan hanya bisa punya **satu
   upgrade pending**; upgrade lain ditolak sampai invoice dibayar atau
   dibatalkan admin.
3. Pembayaran dikonfirmasi ke gateway (callback **selalu diverifikasi ulang**
   via Check Transaction sebelum apapun diaktifkan).

---

## 4. Setelah pembayaran — otomatis sampai spek terpasang

Begitu invoice lunas, sistem berjalan otomatis tanpa campur tangan:

1. **ApplyUpgrade** — layanan di-rebind ke produk/cycle baru,
   `recurring_amount` diperbarui (harga dasar + semua biaya spec), dan
   snapshot pilihan spec (`chosen_specs`) ditulis ke metadata layanan.
2. **Job `provision:change_package`** dijalankan worker (queue critical):
   - Produk custom-spec: paket panel **dibangun ulang dari chosen_specs** —
     `EnsurePackage` membuat/menyamakan paket dinamis (nama deterministik
     dari hash limit + toggle; disk/bandwidth GB dikonversi ke MB, unlimited
     memakai sentinel panel), lalu akun dipindahkan ke paket itu
     (`ChangePackage` WHM / `CMD_API_MODIFY_USER` DirectAdmin).
   - Paket dinamis lama **dihapus otomatis** bila tidak ada layanan lain di
     server yang masih memakainya.
   - Produk flat: akun langsung dipindahkan ke `package_name` produk.
3. **Selesai** — halaman layanan menampilkan harga langganan baru; kuota di
   cPanel/DirectAdmin sudah berubah. Renewal berikutnya memakai harga baru.

Jika job panel gagal (mis. panel down), asynq **retry otomatis**; kegagalan
final memberi alert ke admin dan bisa dipantau/diulang dari
**Admin → Module Queue**.

---

## 5. Ringkasan teknis (untuk developer)

- Endpoint: `POST /api/v1/services/:id/upgrade`
  `{product_id, cycle, specs?: [{key, qty, unlimited}]}` (client) — admin
  memakai `POST /admin/services/:id/upgrade` dengan body yang sama.
- Pending upgrade: `services.pending_upgrade` (JSON `domain.ServiceUpgrade`,
  kini menyertakan `specs` terselesaikan + amount per knob); item invoice
  `related_type=service_upgrade`.
- Validasi/harga spec memakai aturan yang sama dengan checkout order
  (`resolveUpgradeSpecs` di modul provisioning ≙ `orders.priceSpec`).
- E2E: `frontend/tests/e2e/upgrade-custom-spec.spec.ts` menjalankan seluruh
  alur ini melawan mockserver (WHM mock + Duitku mock).
- Kontrak lengkap: `docs/CONTRACTS.md` §9 (services-client) dan
  `docs/MODULES.md` (work order provisioning).

---

## 6. Troubleshooting

| Gejala | Penyebab / solusi |
|---|---|
| Invoice upgrade kecil (< Rp10.000) tidak menampilkan VA/kartu | Normal: Duitku menolak transaksi non-QRIS di bawah Rp10.000 ("Minimum Payment 10000 IDR"), jadi channel tersebut disembunyikan. Bayar via **QRIS**, transfer bank manual, atau saldo kredit. |
| Tombol Upgrade tidak ada | Layanan tidak `active`, atau masih ada upgrade pending (bayar/batalkan invoicenya dulu). |
| Produk tujuan tidak muncul di picker | Produk hidden, atau module panelnya beda dengan layanan berjalan. |
| "product is not priced for this cycle" | Harga dasar produk tujuan belum diisi untuk cycle tsb. (admin). |
| "invalid spec selections" | Qty di luar min/max/step, unlimited pada knob yang tidak mengizinkannya, atau key spec tidak dikenal. |
| Spek belum berubah di panel setelah bayar | Cek Admin → Module Queue (job `provision:change_package` mungkin retry karena panel tidak terjangkau). |
| Kuota panel tidak sesuai | Pastikan `provision_key`/`unit` knob benar (disk/bandwidth GB→MB otomatis). |
