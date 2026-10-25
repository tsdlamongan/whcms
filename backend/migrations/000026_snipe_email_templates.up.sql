-- Domain sniper email templates (id + en): snipe_active, snipe_won,
-- snipe_expired, snipe_failed, snipe_cancelled. Same inline-style markup as
-- migration 000020 so they render through the global "_layout" wrapper.
-- Variables: .Name, .Domain, .FrontendURL, .CompanyName (auto), plus
-- .DeadlineAt (active), .ExpiryDate (won) and an optional
-- preformatted rupiah .RefundAmount shown only when set - see notifications/dto.go.
INSERT INTO email_templates (key, locale, subject, body_html, body_text) VALUES
('snipe_active', 'id', 'Perburuan domain {{.Domain}} dimulai',
 '<h1 class="em-h1" style="margin:0 0 16px;font:600 22px/1.3 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#222222;">Perburuan dimulai</h1><p style="margin:0px 0 14px;font:400 15px/1.65 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#3c3c3c;">Halo {{.Name}},</p><table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="margin:20px 0;background:#eef4fb;border-left:4px solid #336699;border-radius:3px;"><tr><td style="padding:13px 16px;font:400 14px/1.6 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#1f3d5c;">Pembayaran diterima. Kami mulai memburu domain <strong>{{.Domain}}</strong>{{if .DeadlineAt}} hingga <strong>{{.DeadlineAt}}</strong>{{end}}.</td></tr></table><p style="margin:0px 0 14px;font:400 15px/1.65 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#3c3c3c;">Kami akan memberi kabar begitu domain berhasil didapatkan. Jika tidak berhasil sampai batas waktu, harga domain dikembalikan ke saldo credit Anda.</p><table role="presentation" class="em-btn" cellpadding="0" cellspacing="0" border="0" style="margin:24px 0 6px;"><tr><td align="center" style="border-radius:4px;background:#336699;"><a href="{{.FrontendURL}}/snipes" style="display:inline-block;padding:13px 30px;font:600 15px/1 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#ffffff;text-decoration:none;border-radius:4px;">Lihat Perburuan</a></td></tr></table>',
 'Halo {{.Name}},

Pembayaran diterima. Kami mulai memburu domain {{.Domain}}{{if .DeadlineAt}} hingga {{.DeadlineAt}}{{end}}.

Jika tidak berhasil sampai batas waktu, harga domain dikembalikan ke saldo credit Anda.

Pantau di: {{.FrontendURL}}/snipes

-- {{.CompanyName}}'),
('snipe_active', 'en', 'The hunt for {{.Domain}} has started',
 '<h1 class="em-h1" style="margin:0 0 16px;font:600 22px/1.3 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#222222;">The hunt has started</h1><p style="margin:0px 0 14px;font:400 15px/1.65 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#3c3c3c;">Hello {{.Name}},</p><table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="margin:20px 0;background:#eef4fb;border-left:4px solid #336699;border-radius:3px;"><tr><td style="padding:13px 16px;font:400 14px/1.6 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#1f3d5c;">Payment received. We are now hunting <strong>{{.Domain}}</strong>{{if .DeadlineAt}} until <strong>{{.DeadlineAt}}</strong>{{end}}.</td></tr></table><p style="margin:0px 0 14px;font:400 15px/1.65 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#3c3c3c;">We will let you know as soon as the domain is secured. If it is not won by the deadline, the domain price is credited back to your account.</p><table role="presentation" class="em-btn" cellpadding="0" cellspacing="0" border="0" style="margin:24px 0 6px;"><tr><td align="center" style="border-radius:4px;background:#336699;"><a href="{{.FrontendURL}}/snipes" style="display:inline-block;padding:13px 30px;font:600 15px/1 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#ffffff;text-decoration:none;border-radius:4px;">View Hunt</a></td></tr></table>',
 'Hello {{.Name}},

Payment received. We are now hunting {{.Domain}}{{if .DeadlineAt}} until {{.DeadlineAt}}{{end}}.

If it is not won by the deadline, the domain price is credited back to your account.

Follow it at: {{.FrontendURL}}/snipes

-- {{.CompanyName}}'),
('snipe_won', 'id', 'Selamat! Domain {{.Domain}} berhasil didapatkan',
 '<h1 class="em-h1" style="margin:0 0 16px;font:600 22px/1.3 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#222222;">Domain berhasil didapatkan</h1><p style="margin:0px 0 14px;font:400 15px/1.65 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#3c3c3c;">Halo {{.Name}},</p><table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="margin:20px 0;background:#eef8f0;border-left:4px solid #218739;border-radius:3px;"><tr><td style="padding:13px 16px;font:400 14px/1.6 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#14521f;">Domain <strong>{{.Domain}}</strong> berhasil didaftarkan atas nama Anda{{if .ExpiryDate}} dan aktif hingga <strong>{{.ExpiryDate}}</strong>{{end}}.</td></tr></table><p style="margin:0px 0 14px;font:400 15px/1.65 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#3c3c3c;">Domain sudah muncul di akun Anda dan bisa langsung dikelola (nameserver, DNS, dan lainnya).</p><table role="presentation" class="em-btn" cellpadding="0" cellspacing="0" border="0" style="margin:24px 0 6px;"><tr><td align="center" style="border-radius:4px;background:#336699;"><a href="{{.FrontendURL}}/domains" style="display:inline-block;padding:13px 30px;font:600 15px/1 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#ffffff;text-decoration:none;border-radius:4px;">Kelola Domain</a></td></tr></table>',
 'Halo {{.Name}},

Domain {{.Domain}} berhasil didaftarkan atas nama Anda{{if .ExpiryDate}} dan aktif hingga {{.ExpiryDate}}{{end}}.

Kelola domain Anda di: {{.FrontendURL}}/domains

-- {{.CompanyName}}'),
('snipe_won', 'en', 'Success! You secured {{.Domain}}',
 '<h1 class="em-h1" style="margin:0 0 16px;font:600 22px/1.3 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#222222;">You got the domain</h1><p style="margin:0px 0 14px;font:400 15px/1.65 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#3c3c3c;">Hello {{.Name}},</p><table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="margin:20px 0;background:#eef8f0;border-left:4px solid #218739;border-radius:3px;"><tr><td style="padding:13px 16px;font:400 14px/1.6 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#14521f;">The domain <strong>{{.Domain}}</strong> was registered in your name{{if .ExpiryDate}} and is active until <strong>{{.ExpiryDate}}</strong>{{end}}.</td></tr></table><p style="margin:0px 0 14px;font:400 15px/1.65 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#3c3c3c;">It now appears in your account and can be managed right away (nameservers, DNS and more).</p><table role="presentation" class="em-btn" cellpadding="0" cellspacing="0" border="0" style="margin:24px 0 6px;"><tr><td align="center" style="border-radius:4px;background:#336699;"><a href="{{.FrontendURL}}/domains" style="display:inline-block;padding:13px 30px;font:600 15px/1 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#ffffff;text-decoration:none;border-radius:4px;">Manage Domain</a></td></tr></table>',
 'Hello {{.Name}},

The domain {{.Domain}} was registered in your name{{if .ExpiryDate}} and is active until {{.ExpiryDate}}{{end}}.

Manage your domain at: {{.FrontendURL}}/domains

-- {{.CompanyName}}'),
('snipe_expired', 'id', 'Perburuan domain {{.Domain}} berakhir tanpa hasil',
 '<h1 class="em-h1" style="margin:0 0 16px;font:600 22px/1.3 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#222222;">Perburuan berakhir</h1><p style="margin:0px 0 14px;font:400 15px/1.65 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#3c3c3c;">Halo {{.Name}},</p><table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="margin:20px 0;background:#fff7ec;border-left:4px solid #f89406;border-radius:3px;"><tr><td style="padding:13px 16px;font:400 14px/1.6 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#7a4a06;">Masa perburuan domain <strong>{{.Domain}}</strong> telah berakhir dan domain ini belum berhasil kami dapatkan.{{if .RefundAmount}} Pengembalian dana sebesar <strong>{{.RefundAmount}}</strong> sudah masuk ke saldo credit Anda.{{end}}</td></tr></table><p style="margin:0px 0 14px;font:400 15px/1.65 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#3c3c3c;">Terima kasih sudah memakai layanan kami. Anda bisa membuat perburuan baru kapan saja.</p><table role="presentation" class="em-btn" cellpadding="0" cellspacing="0" border="0" style="margin:24px 0 6px;"><tr><td align="center" style="border-radius:4px;background:#336699;"><a href="{{.FrontendURL}}/snipes" style="display:inline-block;padding:13px 30px;font:600 15px/1 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#ffffff;text-decoration:none;border-radius:4px;">Perburuan Saya</a></td></tr></table>',
 'Halo {{.Name}},

Masa perburuan domain {{.Domain}} telah berakhir dan domain ini belum berhasil kami dapatkan.

{{if .RefundAmount}}Pengembalian dana sebesar {{.RefundAmount}} sudah masuk ke saldo credit Anda.

{{end}}Anda bisa membuat perburuan baru kapan saja di: {{.FrontendURL}}/snipes

-- {{.CompanyName}}'),
('snipe_expired', 'en', 'The hunt for {{.Domain}} ended without a result',
 '<h1 class="em-h1" style="margin:0 0 16px;font:600 22px/1.3 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#222222;">The hunt has ended</h1><p style="margin:0px 0 14px;font:400 15px/1.65 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#3c3c3c;">Hello {{.Name}},</p><table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="margin:20px 0;background:#fff7ec;border-left:4px solid #f89406;border-radius:3px;"><tr><td style="padding:13px 16px;font:400 14px/1.6 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#7a4a06;">The hunting window for <strong>{{.Domain}}</strong> has ended and we could not secure the domain.{{if .RefundAmount}} A refund of <strong>{{.RefundAmount}}</strong> has been added to your account credit.{{end}}</td></tr></table><p style="margin:0px 0 14px;font:400 15px/1.65 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#3c3c3c;">Thank you for using our service. You can start a new hunt at any time.</p><table role="presentation" class="em-btn" cellpadding="0" cellspacing="0" border="0" style="margin:24px 0 6px;"><tr><td align="center" style="border-radius:4px;background:#336699;"><a href="{{.FrontendURL}}/snipes" style="display:inline-block;padding:13px 30px;font:600 15px/1 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#ffffff;text-decoration:none;border-radius:4px;">My Hunts</a></td></tr></table>',
 'Hello {{.Name}},

The hunting window for {{.Domain}} has ended and we could not secure the domain.

{{if .RefundAmount}}A refund of {{.RefundAmount}} has been added to your account credit.

{{end}}You can start a new hunt at any time at: {{.FrontendURL}}/snipes

-- {{.CompanyName}}'),
('snipe_failed', 'id', 'Perburuan domain {{.Domain}} dihentikan',
 '<h1 class="em-h1" style="margin:0 0 16px;font:600 22px/1.3 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#222222;">Perburuan dihentikan</h1><p style="margin:0px 0 14px;font:400 15px/1.65 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#3c3c3c;">Halo {{.Name}},</p><table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="margin:20px 0;background:#fdf1f0;border-left:4px solid #c43c35;border-radius:3px;"><tr><td style="padding:13px 16px;font:400 14px/1.6 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#7b2622;">Perburuan domain <strong>{{.Domain}}</strong> dihentikan karena kendala pada proses pendaftaran.{{if .RefundAmount}} Pengembalian dana sebesar <strong>{{.RefundAmount}}</strong> sudah masuk ke saldo credit Anda.{{end}}</td></tr></table><p style="margin:0px 0 14px;font:400 15px/1.65 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#3c3c3c;">Jika Anda butuh bantuan, silakan hubungi tim kami.</p><table role="presentation" class="em-btn" cellpadding="0" cellspacing="0" border="0" style="margin:24px 0 6px;"><tr><td align="center" style="border-radius:4px;background:#336699;"><a href="{{.FrontendURL}}/snipes" style="display:inline-block;padding:13px 30px;font:600 15px/1 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#ffffff;text-decoration:none;border-radius:4px;">Perburuan Saya</a></td></tr></table>',
 'Halo {{.Name}},

Perburuan domain {{.Domain}} dihentikan karena kendala pada proses pendaftaran.

{{if .RefundAmount}}Pengembalian dana sebesar {{.RefundAmount}} sudah masuk ke saldo credit Anda.

{{end}}Jika Anda butuh bantuan, silakan hubungi tim kami.

-- {{.CompanyName}}'),
('snipe_failed', 'en', 'The hunt for {{.Domain}} was stopped',
 '<h1 class="em-h1" style="margin:0 0 16px;font:600 22px/1.3 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#222222;">The hunt was stopped</h1><p style="margin:0px 0 14px;font:400 15px/1.65 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#3c3c3c;">Hello {{.Name}},</p><table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="margin:20px 0;background:#fdf1f0;border-left:4px solid #c43c35;border-radius:3px;"><tr><td style="padding:13px 16px;font:400 14px/1.6 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#7b2622;">The hunt for <strong>{{.Domain}}</strong> was stopped because the registration could not be completed.{{if .RefundAmount}} A refund of <strong>{{.RefundAmount}}</strong> has been added to your account credit.{{end}}</td></tr></table><p style="margin:0px 0 14px;font:400 15px/1.65 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#3c3c3c;">If you need help, please contact our team.</p><table role="presentation" class="em-btn" cellpadding="0" cellspacing="0" border="0" style="margin:24px 0 6px;"><tr><td align="center" style="border-radius:4px;background:#336699;"><a href="{{.FrontendURL}}/snipes" style="display:inline-block;padding:13px 30px;font:600 15px/1 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#ffffff;text-decoration:none;border-radius:4px;">My Hunts</a></td></tr></table>',
 'Hello {{.Name}},

The hunt for {{.Domain}} was stopped because the registration could not be completed.

{{if .RefundAmount}}A refund of {{.RefundAmount}} has been added to your account credit.

{{end}}If you need help, please contact our team.

-- {{.CompanyName}}'),
('snipe_cancelled', 'id', 'Perburuan domain {{.Domain}} dibatalkan',
 '<h1 class="em-h1" style="margin:0 0 16px;font:600 22px/1.3 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#222222;">Perburuan dibatalkan</h1><p style="margin:0px 0 14px;font:400 15px/1.65 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#3c3c3c;">Halo {{.Name}},</p><table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="margin:20px 0;background:#f4f4f4;border-left:4px solid #8a8a8a;border-radius:3px;"><tr><td style="padding:13px 16px;font:400 14px/1.6 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#444444;">Perburuan domain <strong>{{.Domain}}</strong> telah dibatalkan.{{if .RefundAmount}} Pengembalian dana sebesar <strong>{{.RefundAmount}}</strong> sudah masuk ke saldo credit Anda.{{end}}</td></tr></table><p style="margin:0px 0 14px;font:400 15px/1.65 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#3c3c3c;">Jika ini tidak Anda harapkan, silakan hubungi tim kami.</p><table role="presentation" class="em-btn" cellpadding="0" cellspacing="0" border="0" style="margin:24px 0 6px;"><tr><td align="center" style="border-radius:4px;background:#336699;"><a href="{{.FrontendURL}}/snipes" style="display:inline-block;padding:13px 30px;font:600 15px/1 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#ffffff;text-decoration:none;border-radius:4px;">Perburuan Saya</a></td></tr></table>',
 'Halo {{.Name}},

Perburuan domain {{.Domain}} telah dibatalkan.

{{if .RefundAmount}}Pengembalian dana sebesar {{.RefundAmount}} sudah masuk ke saldo credit Anda.

{{end}}Jika ini tidak Anda harapkan, silakan hubungi tim kami.

-- {{.CompanyName}}'),
('snipe_cancelled', 'en', 'The hunt for {{.Domain}} was cancelled',
 '<h1 class="em-h1" style="margin:0 0 16px;font:600 22px/1.3 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#222222;">The hunt was cancelled</h1><p style="margin:0px 0 14px;font:400 15px/1.65 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#3c3c3c;">Hello {{.Name}},</p><table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="margin:20px 0;background:#f4f4f4;border-left:4px solid #8a8a8a;border-radius:3px;"><tr><td style="padding:13px 16px;font:400 14px/1.6 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#444444;">The hunt for <strong>{{.Domain}}</strong> has been cancelled.{{if .RefundAmount}} A refund of <strong>{{.RefundAmount}}</strong> has been added to your account credit.{{end}}</td></tr></table><p style="margin:0px 0 14px;font:400 15px/1.65 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#3c3c3c;">If this was unexpected, please contact our team.</p><table role="presentation" class="em-btn" cellpadding="0" cellspacing="0" border="0" style="margin:24px 0 6px;"><tr><td align="center" style="border-radius:4px;background:#336699;"><a href="{{.FrontendURL}}/snipes" style="display:inline-block;padding:13px 30px;font:600 15px/1 -apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Arial,sans-serif;color:#ffffff;text-decoration:none;border-radius:4px;">My Hunts</a></td></tr></table>',
 'Hello {{.Name}},

The hunt for {{.Domain}} has been cancelled.

{{if .RefundAmount}}A refund of {{.RefundAmount}} has been added to your account credit.

{{end}}If this was unexpected, please contact our team.

-- {{.CompanyName}}')
ON CONFLICT (key, locale) DO NOTHING;
